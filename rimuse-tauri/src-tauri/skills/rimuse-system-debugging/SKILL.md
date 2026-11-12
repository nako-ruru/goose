---
name: rimuse-system-debugging
description: Debug MCP/connection failures, extension install errors, and shell command failures. Use when you see "connection refused", "error sending request", "ECONNREFUSED", "failed to initialize MCP client", or when shell commands fail with 'not recognized' / command not found — especially before fetching any web documentation.
---

# System debugging playbook

## Step 0: Detect the shell dialect (Windows)

This product runs on Windows where the default shell is **cmd.exe**:

- Available: `dir`, `findstr`, `where`, `curl.exe`, `powershell -Command "..."`
- NOT available: `grep`, `head`, `tail`, `ls`, `awk`, `sed` — do not try them,
  and do not retry the same command in another dialect after failure
- For pipes or complex logic, wrap the whole thing: `powershell -Command "..."`

## Step 1: Connection / MCP errors — test before you think

For errors like `connection refused`, `error sending request for url (...)`,
`ECONNREFUSED`, `failed to initialize MCP client`:

1. **Extract the URL from the error message itself** — it is already there, e.g.
   `http://127.0.0.1:PORT/mcp`. Do not guess ports.
2. `curl.exe -sv <URL>` — is it refused, timeout, or an HTTP error?
3. `netstat -ano | findstr <PORT>` — is anything listening?
4. Read the extension's config (the `uri` or `cmd` field) to learn which
   service *should* be providing that endpoint, then check whether *that*
   process is running.
5. Only after steps 1-4 produce a conclusion, decide: restart service /
   fix config / report the fact.

**Never start with fetching web documentation.** Docs cannot tell you whether
a local port is open. The answer is always in steps 2-3.

## Step 2: Treat boilerplate error text as untrusted

Generic appended hints such as "trouble with the network configuration -
VPNs like WARP often cause issues" are **template fallbacks, not diagnoses**.
They are appended to many unrelated failures. Base your conclusion only on
the measurements from Step 1.

## Step 3: Report facts, not hypotheses

When a service is not running, say exactly that:
"port <N> has no listener; process <X> that should provide it is not running".
Do not improvise explanations (VPN, firewall, extension bugs) that the
 measurements do not support.
