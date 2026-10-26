# Alert analysis

When a monitor goes down, Eyrie can ask a language model what most likely
went wrong and what to check first. The down alert goes out at once, as
without analysis; the answer follows in a second message when the model
has given it:

```text
Shop website is down
Status 502 Bad Gateway
https://eyrie.example.com/monitors/3
```

```text
Shop website is down: analysis
Likely cause: response times climbed from 120 ms to 2.9 s over the three checks before the failure, then the proxy returned 502: the application behind it is likely overloaded or has crashed.
Check first: the application's process and memory, then the proxy's upstream logs.
https://eyrie.example.com/monitors/3
```

It is **off** until an admin turns it on, in **Settings → Alert analysis**.

## Choosing a model

| Model | What you need | Billed to |
| --- | --- | --- |
| **Claude** | An API key from the [Claude Console](https://platform.claude.com) (`sk-ant-api03-…`) | The Console account the key belongs to, per token |
| **Claude with your subscription** | A Claude Pro or Max subscription, and `EYRIE_CLAUDE_CODE=true`: Eyrie downloads [Claude Code](https://code.claude.com) to use it | Your subscription's usage limits |
| **A model on your own server** | A server with an OpenAI-compatible API that Eyrie can reach: [Ollama](https://ollama.com), LM Studio, llama.cpp's `llama-server`, vLLM, LocalAI | Nobody: it runs on your hardware |

### Claude

Paste the API key and save. It is stored encrypted with the instance key,
like other secrets, and never sent back to the browser: the form shows
"Saved; leave blank to keep it". Tick **Remove the saved API key** to
delete it.

A subscription token (`sk-ant-oat…`, from `claude setup-token`) is
refused here: Anthropic allows subscription sign-ins only in its own apps.
Use it with Claude Code, below.

### Claude with your subscription (Claude Code)

Anthropic allows a Claude Pro or Max subscription only in its own apps,
such as Claude Code. So Eyrie downloads the unmodified Claude Code from
Anthropic and asks Claude through it; Claude Code holds the sign-in, and
Eyrie never reads it.

1. On any computer with a browser, install Claude Code and run
   `claude setup-token`. Sign in with your subscription; it prints a token
   that lasts a year.
2. Give Eyrie two environment variables, such as in `compose.yaml`, and
   restart it:

   ```yaml
       environment:
         EYRIE_CLAUDE_CODE: "true"
         CLAUDE_CODE_OAUTH_TOKEN: <the token>
   ```

3. In **Settings → Alert analysis**, choose **Claude, with your Claude Pro
   or Max subscription**, then **Test with a made-up alert**.

At start, Eyrie downloads Claude Code's stable release from
`https://downloads.claude.ai/claude-code-releases`, the way Anthropic's
installer does: it reads the version, then the release's manifest, and
keeps the binary only if its size and SHA-256 match the manifest. It is
about 300 MB, kept in `<data folder>/claude-code/<version>/claude`, so a
restart does not download it again. Eyrie starts without waiting for it;
the settings page shows when it is ready. Every day Eyrie checks for a
new stable release and replaces the old one; after a failed download it
tries again every hour, and a working older version stays in use. Claude
Code does not update itself, and keeps its own settings in
`<data folder>/claude-code/config`.

Claude Code runs on Linux (x86-64 and Arm, glibc or musl) and macOS. The
published image includes the C library it needs.

Each analysis runs Claude Code with no tools, no MCP servers, no slash
commands and no saved session, in a new empty folder that is deleted
afterwards:

```sh
claude -p --output-format json --max-turns 1 --tools "" --strict-mcp-config \
  --disable-slash-commands --no-session-persistence --system-prompt "<Eyrie's instructions>" \
  [--model <model>]   # the alert's data on standard input
```

The models are Claude Code's aliases, `opus`, `sonnet` and `haiku`, or
your account's default.

### A model on your own server

The model can run on another machine, such as a Mac with Ollama, while
Eyrie runs somewhere else. Set:

- **Server URL**: the server's OpenAI-compatible API, ending in `/v1`.
  Eyrie sends `POST <server URL>/chat/completions`.

  | Server | Default port | Server URL |
  | --- | --- | --- |
  | Ollama | 11434 | `http://192.0.2.20:11434/v1` |
  | LM Studio | 1234 | `http://192.0.2.20:1234/v1` |
  | llama.cpp (`llama-server`) | 8080 | `http://192.0.2.20:8080/v1` |
  | vLLM | 8000 | `http://192.0.2.20:8000/v1` |

- **Model**: required. The settings page lists the server's models
  (`GET <server URL>/models`) as soon as the server URL is set; choose one.
  If the list cannot be loaded, it shows why and takes a name typed in,
  such as `qwen3:14b` for Ollama.
- **API key**: only if the server asks for one (vLLM's `--api-key`, a
  proxy in front). It is sent as `Authorization: Bearer <key>`, stored
  encrypted, and kept separately from the Claude API key.

Make the server listen on the network, not only on its own machine:

- **Ollama** listens on `127.0.0.1` by default. On a Mac running the
  Ollama app, run `launchctl setenv OLLAMA_HOST "0.0.0.0"` and restart the
  app; on Linux, set `OLLAMA_HOST=0.0.0.0` in its systemd service
  (`systemctl edit ollama`). Ollama has no sign-in: allow only Eyrie's
  host to reach the port, in the machine's firewall.
- **LM Studio**: in the Developer tab, turn on **Serve on Local Network**.
- **llama.cpp**: start `llama-server` with `--host 0.0.0.0`.
- **Eyrie in Docker, the model on the same machine**: use
  `http://host.docker.internal:11434/v1`. On Linux, also give the
  container `--add-host=host.docker.internal:host-gateway` (Compose:
  `extra_hosts: ["host.docker.internal:host-gateway"]`).

The monitor's data crosses your network to the server: use `https://`
when it crosses one you do not trust.

Some models write their reasoning between `<think>` and `</think>` before
the answer; Eyrie leaves that out. A small model on a CPU can take longer
than the default 60 seconds: raise the timeout, and use **Test with a
made-up alert** to see how long it takes.

## Settings

| Setting | Meaning |
| --- | --- |
| Analyse down alerts | Off by default. |
| Analyse with | Claude with an API key, Claude with your subscription (only with `EYRIE_CLAUDE_CODE=true`), or a model on your own server. |
| Server URL | Your own server only. Required to turn analysis on. |
| API key | Claude with an API key: required to turn analysis on. Your own server: only if it needs one. Not used with your subscription. |
| Model | Chosen from the models the provider lists (Claude: `GET /v1/models` with the API key; your own server: `GET <server URL>/models`), or typed in when the list cannot be loaded. Claude: blank is `claude-opus-5-5`. Your subscription: `opus`, `sonnet`, `haiku`, or blank for your account's default. Your own server: required. |
| Timeout | 5 to 300 seconds, 60 by default: how long Eyrie waits for the model to answer. |

**Test with a made-up alert** sends a made-up down alert (a shop website
answering 502 after slow responses) with the form as it is, saved or not,
and shows the answer or the error. It then sends the alert, with the
answer, to the channel chosen in **Send the test alert to** (at first an
enabled default channel, or any enabled one): the alert before asking the
model, then the analysis in its own message, as on a real alert. Choose **Nowhere** to only see it on the page.

## What happens on an alert

- Only **down** alerts (`monitor_down`) are analysed. "Still down"
  reminders, "back up" messages, certificate warnings and tests are not.
- The down alert is sent at once and never waits for the analysis. The
  analysis follows in its own message (`monitor_analysis`, priority 3,
  titled `<monitor> is down: analysis`) to the same channels, when the
  model answers within the timeout. Without an answer (an error, no answer
  in time, the model declining), no analysis is sent, and the failure is
  logged (`alert analysis failed`).
- Nothing waits for the analysis: when the monitor is back up before the
  model answers, "back up" arrives first and the analysis after it.
- At most four analyses run at once. When more monitors go down together,
  as in a network outage, the others get no analysis
  (`alert analysis skipped: too many at once`).
- The analysis is at most 800 characters. The model is asked for the
  same form every time, 300 to 500 characters of plain text without
  Markdown, in two lines: `Likely cause: …` (with the evidence from the
  data) and `Check first: …`. Models on your own server are asked with
  temperature 0, so their answers stay alike. It is the analysis message's
  text. See [Notifications](notifications.md#message-formats).
- The metric `eyrie_alert_analyses_total{result="ok"|"failed"}` counts them.

With Claude and a model that supports it (`claude-opus-5-5`,
`claude-opus-5`, `claude-fable-5-1`, `claude-sonnet-5-5`), requests ask for
Anthropic's server-side fallback: if the model's safety classifiers decline
the request, the model Anthropic recommends answers instead.

## What is sent to the model

The monitor's:

- name, type, group and description;
- schedule: interval, timeout, retries;
- settings, without anything that can be a credential. An HTTP monitor's
  URL is sent without its user, password, query and fragment; of its
  request headers, only the names; of a request body, only that there is
  one. A TCP monitor's host and port, a ping monitor's host, a DNS
  monitor's name, record type and server;
- the failing check's message and time;
- the last 24 hours' uptime and average response time;
- the last 30 checks of the last 24 hours: time, status, response time and
  message.

Check messages can contain text from the monitored service, such as the
start of a page without the keyword. The model is told that everything
about the monitor is data, not instructions.

With Claude, requests go to `https://api.anthropic.com/v1/messages` under
the key's Claude Console account, or through Claude Code under your
subscription, and
[Anthropic's terms and privacy policy](https://www.anthropic.com/legal)
apply to what is sent. With your own server, they go only to the server
URL.
