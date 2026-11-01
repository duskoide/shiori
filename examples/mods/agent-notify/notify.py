#!/usr/bin/env python3
"""A Shiori mod's service: tells the desktop when an agent needs you.

It connects to the window's control socket, says hello as the mod with the
token it was started with, and gets the events its mod.json names. Only the
standard library; notify-send (Linux) or osascript (macOS) shows the
notification.
"""
import json
import os
import shutil
import socket
import subprocess
import sys

sock = socket.socket(socket.AF_UNIX)
sock.connect(os.environ["SHIORI_SOCKET"])
stream = sock.makefile("rw", encoding="utf-8")


def send(msg):
    stream.write(json.dumps({"jsonrpc": "2.0", **msg}) + "\n")
    stream.flush()


def notify(title, body):
    if shutil.which("notify-send"):
        subprocess.run(["notify-send", title, body], check=False)
    elif shutil.which("osascript"):
        script = "display notification %s with title %s" % (json.dumps(body), json.dumps(title))
        subprocess.run(["osascript", "-e", script], check=False)
    else:
        print(f"{title}: {body}", file=sys.stderr)


send({"id": 1, "method": "hello", "params": {
    "api": 1, "mod": os.environ["SHIORI_MOD"], "token": os.environ["SHIORI_MOD_TOKEN"]}})
reply = json.loads(stream.readline())
if "error" in reply:
    sys.exit("hello: " + reply["error"]["message"])

window_in_front = True
for line in stream:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "shutdown":
        break
    if method != "event":
        continue
    name, data = msg["params"]["name"], msg["params"]["data"]
    if name == "window.focus":
        window_in_front = data["focused"]
    elif name == "agent.status" and data["status"] == "blocked" and not window_in_front:
        notify("An agent needs you", data.get("app") or "agent")
