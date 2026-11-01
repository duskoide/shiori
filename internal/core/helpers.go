package core

import (
	"context"
	"encoding/json"
	"errors"
)

// Run starts a terminal and returns its address. Env defaults to
// TERM=xterm-256color, COLORTERM=truecolor, TERM_PROGRAM=herdr (explicit
// entries win).
func (c *Client) Run(ctx context.Context, p RunParams) (string, error) {
	env := map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor", "TERM_PROGRAM": "herdr"}
	for k, v := range p.Env {
		env[k] = v
	}
	argv := p.Argv
	if argv == nil {
		argv = []string{}
	}
	params := map[string]any{"argv": argv, "env": env, "keep": p.Keep}
	if p.Cwd != "" {
		params["cwd"] = p.Cwd
	}
	if p.Labels != nil {
		params["labels"] = p.Labels
	}
	if p.On != "" {
		params["on"] = p.On
	}
	if p.Name != "" {
		params["name"] = p.Name
	}
	var res struct {
		Address string `json:"address"`
	}
	if err := c.Call(ctx, "terminal.run", params, &res); err != nil {
		return "", err
	}
	if res.Address == "" {
		return "", errors.New("herdr-core: terminal.run returned no address")
	}
	return res.Address, nil
}

// Kill kills a terminal.
func (c *Client) Kill(ctx context.Context, address string) error {
	return c.Call(ctx, "terminal.kill", map[string]any{"terminal": address}, nil)
}

// Label sets labels on a terminal; an empty value removes the label.
func (c *Client) Label(ctx context.Context, address string, labels map[string]string) error {
	if labels == nil {
		labels = map[string]string{}
	}
	return c.Call(ctx, "terminal.label", map[string]any{"terminal": address, "labels": labels}, nil)
}

// Keep sets whether a terminal is restarted after its program is lost.
func (c *Client) Keep(ctx context.Context, address string, keep bool) error {
	return c.Call(ctx, "terminal.keep", map[string]any{"terminal": address, "keep": keep}, nil)
}

// Get returns one terminal record (live or finished).
func (c *Client) Get(ctx context.Context, address string) (Terminal, error) {
	var t Terminal
	err := c.Call(ctx, "terminal.get", map[string]any{"terminal": address}, &t)
	return t, err
}

// DocGet reads a document. A never-written document has value null, version 0.
func (c *Client) DocGet(ctx context.Context, name, on string) (json.RawMessage, uint64, error) {
	params := map[string]any{"name": name}
	if on != "" {
		params["on"] = on
	}
	var res struct {
		Value   json.RawMessage `json:"value"`
		Version uint64          `json:"version"`
	}
	if err := c.Call(ctx, "doc.get", params, &res); err != nil {
		return nil, 0, err
	}
	return res.Value, res.Version, nil
}

// DocSet writes a document if version matches the current one; otherwise it
// fails with an *RPCError of Kind "conflict".
func (c *Client) DocSet(ctx context.Context, name, on string, version uint64, value json.RawMessage) (uint64, error) {
	if len(value) == 0 {
		value = json.RawMessage("null")
	}
	params := map[string]any{"name": name, "version": version, "value": value}
	if on != "" {
		params["on"] = on
	}
	var res struct {
		Version uint64 `json:"version"`
	}
	if err := c.Call(ctx, "doc.set", params, &res); err != nil {
		return 0, err
	}
	return res.Version, nil
}

// Machines lists the machines known to the core.
func (c *Client) Machines(ctx context.Context) ([]Machine, error) {
	var res struct {
		Machines []Machine `json:"machines"`
	}
	if err := c.Call(ctx, "machine.list", nil, &res); err != nil {
		return nil, err
	}
	return res.Machines, nil
}

// Status reports the core process: its pid and how long it has run.
func (c *Client) Status(ctx context.Context) (ServerStatus, error) {
	var s ServerStatus
	err := c.Call(ctx, "server.status", map[string]any{}, &s)
	return s, err
}
