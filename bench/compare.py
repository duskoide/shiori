#!/usr/bin/env python3
"""Compare herdr-app and herdr-mygo: startup, memory, idle CPU and output throughput.

Each run uses its own app home and its own herdr-core session (started before the
app, so startup measures the app only). The user's real sessions are not touched.

    python3 bench/compare.py [runs] [--json out.json] [--only herdr-mygo]
    HERDR_MYGO_BIN=/path/to/build picks another herdr-mygo binary.
"""
import json, os, shutil, statistics, subprocess, sys, tempfile, time

HERE = os.path.dirname(os.path.abspath(__file__))
APPS = {
    "herdr-app": {"bin": shutil.which("herdr-app") or os.path.expanduser("~/.local/bin/herdr-app"), "home": "HERDR_APP_HOME"},
    "herdr-mygo": {"bin": os.environ.get("HERDR_MYGO_BIN") or os.path.join(HERE, "..", "herdr-mygo"), "home": "HERDR_MYGO_HOME"},
}
TICK = os.sysconf("SC_CLK_TCK")
FLOOD_ONE = 2_000_000   # lines printed into one pane
FLOOD_EACH = 500_000    # lines printed into each of 4 panes at once


def children_map():
    m = {}
    for d in os.listdir("/proc"):
        if d.isdigit():
            try:
                with open(f"/proc/{d}/stat") as f:
                    ppid = int(f.read().rsplit(")", 1)[1].split()[1])
                m.setdefault(ppid, []).append(int(d))
            except (OSError, ValueError):
                pass
    return m


def tree(pid):
    m, out, i = children_map(), [pid], 0
    while i < len(out):
        out += m.get(out[i], [])
        i += 1
    return out


def cpu_secs(pids):
    t = 0
    for p in pids:
        try:
            with open(f"/proc/{p}/stat") as f:
                s = f.read().rsplit(")", 1)[1].split()
            t += int(s[11]) + int(s[12])
        except OSError:
            pass
    return t / TICK


def mem(pids):
    r = {"rss": 0, "pss": 0, "private": 0}
    for p in pids:
        try:
            with open(f"/proc/{p}/smaps_rollup") as f:
                for line in f:
                    k, _, v = line.partition(":")
                    kb = int(v.split()[0]) if v.split() and v.split()[0].isdigit() else 0
                    if k == "Rss": r["rss"] += kb
                    elif k == "Pss": r["pss"] += kb
                    elif k in ("Private_Clean", "Private_Dirty"): r["private"] += kb
        except OSError:
            pass
    return {k: round(v / 1024, 1) for k, v in r.items()}


def core_pids(session):
    out = []
    for d in os.listdir("/proc"):
        if not d.isdigit():
            continue
        try:
            with open(f"/proc/{d}/cmdline", "rb") as f:
                cmd = f.read().split(b"\0")
            if not cmd[0].endswith(b"herdr-core") or len(cmd) < 2 or cmd[1] not in (b"serve", b"hold"):
                continue
            with open(f"/proc/{d}/environ", "rb") as f:
                if b"HC_SESSION=" + session.encode() in f.read().split(b"\0"):
                    out.append(int(d))
        except OSError:
            pass
    return out


class Run:
    def __init__(self, name, idx):
        self.name, self.a = name, APPS[name]
        self.home = tempfile.mkdtemp(prefix=f"bench-{name}-")
        self.session = f"bench-{name[6:]}-{idx}-{os.getpid()}"
        env = {k: v for k, v in os.environ.items()
               if k not in ("LD_LIBRARY_PATH", "HC_SOCKET", "HC_TERMINAL_SOCKET", "HC_TERMINAL_ID", "HC_SCOPE")}
        env[self.a["home"]] = self.home
        env["HC_SESSION"] = self.session
        self.env = env
        self.res = {}

    def ctl(self, *args):
        return subprocess.run([self.a["bin"], *args], env=self.env, capture_output=True, text=True, timeout=60)

    def hc(self, *args):
        return subprocess.run(["hc", *args], env=self.env, capture_output=True, text=True, timeout=60)

    def terminals(self):
        try:
            return [t["address"] for t in json.loads(self.hc("ls").stdout)["terminals"]]
        except (ValueError, KeyError):
            return []

    def wait_terminals(self, n, limit=30):
        end = time.monotonic() + limit
        while time.monotonic() < end:
            ts = self.terminals()
            if len(ts) >= n:
                return ts
            time.sleep(0.1)
        raise RuntimeError(f"{self.name}: expected {n} terminals, have {self.terminals()}")

    def size(self, t):
        d = json.loads(self.hc("get", t).stdout)
        return f"{d['cols']}x{d['rows']}"

    def pids(self):
        return tree(self.proc.pid)

    def sample(self, secs):
        pids, core = self.pids(), core_pids(self.session)
        c0, k0, t0 = cpu_secs(pids), cpu_secs(core), time.monotonic()
        time.sleep(secs)
        dt = time.monotonic() - t0
        return {"cpu_pct": round(100 * (cpu_secs(pids) - c0) / dt, 2),
                "core_cpu_pct": round(100 * (cpu_secs(core) - k0) / dt, 2),
                **mem(self.pids())}

    def flood(self, terms, lines, idle_pct):
        d = tempfile.mkdtemp(prefix="flood-", dir=self.home)
        pids, core = self.pids(), core_pids(self.session)
        c0, k0, t0 = cpu_secs(pids), cpu_secs(core), time.monotonic()
        for i, t in enumerate(terms):
            self.hc("send", t, f"date +%s.%N > {d}/s{i}; seq 1 {lines}; date +%s.%N > {d}/e{i}")
        while not all(os.path.exists(f"{d}/e{i}") and os.path.getsize(f"{d}/e{i}") for i in range(len(terms))):
            if time.monotonic() - t0 > 300:
                raise RuntimeError("flood timed out")
            time.sleep(0.05)
        rd = lambda p: float(open(p).read())
        produced = max(rd(f"{d}/e{i}") for i in range(len(terms))) - min(rd(f"{d}/s{i}") for i in range(len(terms)))
        # wait until the app has drawn everything: CPU back near idle for 1s
        thresh, last, quiet = idle_pct + 3.0, cpu_secs(pids), 0
        while quiet < 4 and time.monotonic() - t0 < 300:
            time.sleep(0.25)
            now = cpu_secs(pids)
            quiet = quiet + 1 if 100 * (now - last) / 0.25 < thresh else 0
            last = now
        settled = time.monotonic() - t0 - 1.0
        return {"producer_s": round(produced, 2), "settled_s": round(settled, 2),
                "app_cpu_s": round(cpu_secs(pids) - c0, 2),
                "app_cpu_above_idle_s": round(cpu_secs(pids) - c0 - idle_pct / 100 * (settled + 1), 2), "core_cpu_s": round(cpu_secs(core) - k0, 2),
                **mem(self.pids())}

    def go(self):
        r = self.res
        self.hc("server", "start")
        self.log = open(os.path.join(self.home, "app.log"), "w")
        t0 = time.monotonic()
        self.proc = subprocess.Popen([self.a["bin"]], env=self.env, stdout=self.log, stderr=self.log,
                                     start_new_session=True)
        while self.ctl("ls").returncode != 0:
            if time.monotonic() - t0 > 30:
                raise RuntimeError(f"{self.name} did not start")
            time.sleep(0.01)
        r["startup_s"] = round(time.monotonic() - t0, 3)
        time.sleep(3)
        r["empty"] = self.sample(5)

        t = time.monotonic()
        self.ctl("do", "space.new", "--name", "bench")
        self.wait_terminals(1)
        r["first_terminal_s"] = round(time.monotonic() - t, 3)
        time.sleep(5)
        r["idle_1pane"] = self.sample(10)

        one = self.terminals()
        r["size_1pane"] = self.size(one[0])
        # a 2x2 grid
        t = time.monotonic()
        # wait for each new terminal: herdr-mygo replies before the pane exists
        self.ctl("do", "pane.split", "--direction", "right")
        self.wait_terminals(2)
        self.ctl("do", "pane.split", "--direction", "down")
        self.wait_terminals(3)
        time.sleep(0.3)
        self.ctl("do", "pane.go", "--direction", "left")
        self.ctl("do", "pane.split", "--direction", "down")
        terms = self.wait_terminals(4)
        time.sleep(1)
        r["split3_s"] = round(time.monotonic() - t, 3)
        r["size_grid_pane"] = "/".join(sorted({self.size(t) for t in terms}))
        time.sleep(5)
        r["idle_4pane"] = self.sample(10)

        idle = r["idle_4pane"]["cpu_pct"]
        r["flood_1pane"] = self.flood(terms[:1], FLOOD_ONE, idle)
        time.sleep(2)
        r["flood_4pane"] = self.flood(terms, FLOOD_EACH, idle)
        time.sleep(5)
        r["after"] = self.sample(10)
        return r

    def close(self):
        if getattr(self, "proc", None):
            self.proc.terminate()
            try:
                self.proc.wait(5)
            except subprocess.TimeoutExpired:
                self.proc.kill()
        self.hc("server", "stop")
        shutil.rmtree(self.home, ignore_errors=True)
        state = os.environ.get("XDG_STATE_HOME") or os.path.expanduser("~/.local/state")
        shutil.rmtree(os.path.join(state, "herdr-core", self.session), ignore_errors=True)


def flat(d, p=""):
    out = {}
    for k, v in d.items():
        if isinstance(v, dict):
            out.update(flat(v, f"{p}{k}."))
        else:
            out[p + k] = v
    return out


def main():
    args = sys.argv[1:]
    out = None
    if "--json" in args:
        i = args.index("--json"); out = args[i + 1]; del args[i:i + 2]
    if "--only" in args:
        i = args.index("--only")
        for n in list(APPS):
            if n != args[i + 1]:
                del APPS[n]
        del args[i:i + 2]
    runs = int(args[0]) if args else 3
    results = {n: [] for n in APPS}
    for i in range(runs):
        order = list(APPS) if i % 2 == 0 else list(APPS)[::-1]   # alternate to spread thermal drift
        for name in order:
            run = Run(name, i)
            try:
                results[name].append(flat(run.go()))
                print(f"run {i + 1}/{runs} {name}: ok", file=sys.stderr, flush=True)
            finally:
                run.close()
            time.sleep(3)
    if out:
        with open(out, "w") as f:
            json.dump(results, f, indent=1)
    names = list(APPS)
    keys = list(results[names[0]][0])
    def med(n, k):
        vals = [r[k] for r in results[n]]
        return statistics.median(vals) if all(isinstance(v, (int, float)) for v in vals) else "/".join(sorted(set(map(str, vals))))
    print(f"| metric (median of {runs}) | " + " | ".join(names) + " |\n|---|" + "---:|" * len(names))
    for k in keys:
        print(f"| {k} | " + " | ".join(str(med(n, k)) for n in names) + " |")


if __name__ == "__main__":
    main()
