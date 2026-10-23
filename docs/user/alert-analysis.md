# Alert analysis with Claude

When a monitor goes down, Eyrie can ask [Claude](https://www.anthropic.com/claude)
what most likely went wrong and what to check first, and put the answer in
the down alert:

```text
Shop website is down
Status 502 Bad Gateway

Claude's analysis: Response times climbed from 120 ms to 2.9 s over the
three checks before the failure, then the proxy returned 502: the
application behind it is likely overloaded or has crashed. Check the
application's process and memory first, then the proxy's upstream logs.
https://eyrie.example.com/monitors/3
```

It is **off** until an admin turns it on, in **Settings → Alert analysis**.

## Connecting to Claude

Choose one of two ways:

| Connect with | What you need | Billed to |
| --- | --- | --- |
| **An Anthropic API key** | A key from the [Claude Console](https://platform.claude.com) (`sk-ant-api03-…`) | The Console account the key belongs to, per token |
| **Claude Code** | [Claude Code](https://code.claude.com) installed where Eyrie runs, signed in with your Claude Pro or Max subscription | Your subscription's usage limits |

### An API key

Paste the key and save. It is stored encrypted with the instance key, like
other secrets, and never sent back to the browser: the form shows "Saved;
leave blank to keep it". Tick **Remove the saved API key** to delete it.

A Claude subscription token (`sk-ant-oat…`, from `claude setup-token`) is
refused here. Anthropic allows subscription sign-ins only in its own apps,
such as Claude Code, so another app may not store such a token or call
the API with it. To use a subscription, choose Claude Code.

### Claude Code (a Claude subscription)

Eyrie runs the Claude Code binary, unmodified, the way you would run
`claude -p` yourself, and reads its answer. Claude Code holds the sign-in;
Eyrie never sees or stores it.

1. Install Claude Code on the machine Eyrie runs on
   ([setup](https://code.claude.com/docs/en/setup)).
2. Sign it in **as the user Eyrie runs as**: run `claude auth login`, or run
   `claude setup-token` anywhere and set the printed token as
   `CLAUDE_CODE_OAUTH_TOKEN` in Eyrie's environment.
3. If `claude` is not on Eyrie's `PATH`, set `EYRIE_CLAUDE_COMMAND` to its
   full path. The settings page shows where it was found, or why not.

The published container image does not include Claude Code: Anthropic's
terms require it to be installed as Anthropic publishes it. The image is
built on a base without a C library, so the Claude Code binary cannot be
added to it as is. With Docker, use an API key, or run Eyrie's binary
directly on a machine with Claude Code.

Each analysis runs `claude -p` with no tools, no MCP servers, no slash
commands and no saved session, in a new empty folder that is deleted
afterwards, so no project settings load:

```sh
claude -p --output-format json --max-turns 1 --tools "" --strict-mcp-config \
  --disable-slash-commands --no-session-persistence --system-prompt "<Eyrie's instructions>" \
  [--model <model>]   # the alert's data on standard input
```

## Settings

| Setting | Meaning |
| --- | --- |
| Analyse down alerts with Claude | Off by default. |
| Connect with | An API key, or Claude Code. |
| API key | API key only. Required to turn analysis on. |
| Model | A model ID such as `claude-sonnet-5-5`; for Claude Code also an alias such as `opus` or `sonnet`. Blank: `claude-opus-5-5` with an API key, your account's default with Claude Code. |
| Timeout | 5 to 300 seconds, 60 by default: how long a down alert waits for the analysis. |

**Test with a made-up alert** sends a made-up down alert (a shop website
answering 502 after slow responses) with the form as it is, saved or not,
and shows Claude's answer or the error.

## What happens on an alert

- Only **down** alerts (`monitor_down`) are analysed. "Still down"
  reminders, "back up" messages, certificate warnings and tests are not.
- The down alert waits for the analysis, at most the timeout. Without an
  answer by then (an error, no answer in time, Claude declining), the alert
  goes out without one, and the failure is logged (`alert analysis
  failed`).
- The monitor's later messages, such as "back up", wait for the down alert,
  so they never arrive before it. Other monitors' messages do not wait.
- At most four analyses run at once. When more monitors go down together,
  as in a network outage, the others go out without an analysis
  (`alert analysis skipped: too many at once`).
- The analysis is at most 800 characters (Claude is asked for 500, plain
  text, without Markdown). It follows the message as `Claude's analysis:
  …`; email has it as its own paragraph and webhooks as the `analysis`
  field. See [Notifications](notifications.md#message-formats).
- The metric `eyrie_alert_analyses_total{result="ok"|"failed"}` counts them.

With an API key and a model that supports it (`claude-opus-5-5`,
`claude-opus-5`, `claude-fable-5-1`, `claude-sonnet-5-5`), requests ask for
Anthropic's server-side fallback: if the model's safety classifiers decline
the request, the model Anthropic recommends answers instead.

## What is sent to Claude

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
start of a page without the keyword. Claude is told that everything about
the monitor is data, not instructions.

With an API key, requests go to `https://api.anthropic.com/v1/messages`
under the key's Claude Console account. With Claude Code, Claude Code sends
them under your subscription. Either way,
[Anthropic's terms and privacy policy](https://www.anthropic.com/legal)
apply to what is sent.
