# Questions, approvals and terminal dialogs

Three different things can ask you to decide. They look alike in the queue, but they come from
different places and behave differently.

| | Who makes it | What you do | Does the agent stop? |
|---|---|---|---|
| **Question** | The agent, on purpose, with the `ask` tool | Pick an option or write an answer, with an optional note | Usually not: it carries on with what the answer does not block |
| **Approval** | Claude Code, when a tool call needs permission (crowsnest's hook catches it) | **Allow once** or **Deny** (with a note) | Yes, that one call waits |
| **Terminal dialog** | Claude Code's own on-screen menu (permission prompt, folder trust, a form) | Press the option's button; crowsnest presses that key in the pane | Yes |

All three land in the same [attention queue](attention-and-now.md): on Now, in the Inbox, in the
tray on every other view, and as a notification when you are away.

## Questions

An agent asks with `ask` (MCP) or `crowsnest ask`. A question has a title, an optional markdown
body (code, diagrams, a diff, attachments), and one of these kinds:

- **confirm**: yes or no;
- **choice**: pick one option, or several when it says "Pick any number";
- **manual**: write the answer in your own words;
- **ack**: just acknowledge it;
- **notice**: no answer at all (a heads-up; kept apart under **Notices**).

The agent marks the option it recommends; crowsnest shows it as **recommended**. A question is
**blocking** when the agent is waiting on it (or its job has declared it has nothing else to do),
and **can wait** otherwise.

**Asking does not stop the asker.** By default `ask` returns at once and the agent is told to keep
going on whatever the answer does not block. Your answer arrives later as a line typed into its
pane: `[crowsnest answer q_7] <title> → <answer> (note: …)`. A busy agent queues it; an idle one
wakes up. Only an irreversible or expensive step should use `ask(wait=true)`, which blocks.

**Expiry is not consent.** A question that expires is closed, and the agent is told it was not
answered. Nothing is ever approved by silence.

### Receipts

An answer saved is not an answer the agent took in. Each answered question shows how far it got:

| Receipt | Meaning |
|---|---|
| **Stored** | Saved; not typed to the agent yet. |
| **Held** | The agent could not take it yet (it was at a dialog, had text in its input box, or was gone); crowsnest keeps trying. |
| **Delivered** | Typed into the agent; not yet seen in its conversation. |
| **Received** | The agent took it in: the answer line is in its transcript. |
| **Delivery uncertain** | Handed to the terminal, but crowsnest lost herdr's reply. |
| **Not delivered** | Typing it failed. |

crowsnest never retypes an answer by itself (a duplicate answer is worse than a late one). For an
uncertain or failed one, the question offers **Did it arrive?** (looks in the transcript again)
and then, only if it still was not seen, **Type it again**.

## Approvals

When a Claude agent crowsnest started wants to run a tool that needs permission, a
`PermissionRequest` hook turns the request into an **approval**: a Bash command with its
directory, a file edit as a diff, a URL, an MCP call. The agent's terminal shows Claude's own
dialog at the same time; whichever you answer first wins.

- **Allow once** runs that one call. There is no "always allow" from the browser; standing
  permission is either Claude's own "always" in the terminal, or one of crowsnest's [approval
  rules](../how-to/projects.md#approval-rules), which you set on a project's page.
- **Deny** refuses it; your note is passed to the agent, which carries on.
- If anything goes wrong (crowsnest down, the approval expires after 12 hours, the agent moved
  on), the hook prints nothing and Claude's own dialog is still there. It fails to the dialog,
  never to an allow.

### Risk levels

crowsnest rates every approval:

- **Low risk**: the call matched a list of known-harmless shapes (reading files, read-only git,
  `git add` and `commit`, `go test`, the project's test, lint and check scripts). A command is read
  the way a shell would: quotes, `sudo`/`env`/`nohup` wrappers, `sh -c` strings and git's global
  options are taken apart first, and every command in a `&&` or `|` chain must be on the list.
- **Elevated**: anything else. Also an output redirect to a file, a line crowsnest cannot read
  (an unterminated quote, a script from stdin), or a summary too long to show whole.
- **Dangerous**: a call that matched a dangerous rule: a force push, `rm -rf`, `git reset --hard`,
  `git clean -f`, `sudo`, dropping a database table, piping `curl` into a shell, or naming a
  protected (production) host anywhere. **Allow once** asks for a second confirmation, and the
  server checks that confirmation too. Your own rules can be added in
  `~/.config/crowsnest/risk.yaml`.

The level decides what is offered elsewhere: only a low-risk approval whose command fits uncut
gets **Allow** and **Deny** buttons on a notification; an elevated or dangerous one never does,
and an approval rule allows an elevated call only if you said so when you made the rule.

## Terminal dialogs

Some things only appear on the agent's screen: Claude's permission menu when no hook caught it,
the one-time "do you trust this folder?" question, a login prompt. crowsnest reads the pane, shows
the dialog as a card with its options, and presses the key you choose (for example `2`, or `down`
then `Enter`). The press is bound to the exact screen it was read from, so if the screen changed
meanwhile, nothing is pressed and the card refreshes; after it, crowsnest watches the screen and
says so if the same question is still there. crowsnest never answers a dialog by itself.
A form (several fields) opens the terminal instead.

The folder-trust question gets its own card, whichever agent asks and however it draws its menu:
the agent and the folder named, what trusting means, **Trust this folder** and **Don't trust**
([how to avoid it](../troubleshooting/common-problems.md#the-folder-trust-prompt)).

## Gates: questions that grant something

Some questions are crowsnest's own gates: "land this job?", "start this job outside its
directory?", a scheduled task file to approve, or any question whose title or options use a
granting word: allow, approve, approval, merge, land, push, deploy, delete, grant or permit. These are **never answerable from a notification** (no
buttons, and the server refuses one). On the decision screen crowsnest's own gates, and a question
whose title or chosen option uses a permission word (allow, approve, merge, land, push, deploy,
delete, publish, grant, permit), take a second tap before they answer; "apply", "accept", "confirm"
and "proceed" keep a question off notifications and rows but answer in one tap there, where the
evidence is on screen. See [Safety](../safety.md).

## What can go wrong

- **You answered but the agent did not react.** Look at the receipt. **Held** means it is at a
  dialog or has a draft in its input box; clear that and crowsnest delivers it.
- **An approval disappeared.** It was answered in the terminal, or the agent's turn ended
  (approvals are superseded then).
- **The same call keeps asking.** Add an [approval rule](../how-to/projects.md#approval-rules), or
  use **Allow and remember for this job** on the card. That remembers a low-risk call's shape; an
  elevated call only as exactly itself; a call crowsnest does not recognise not at all (write a
  rule on the project page for those).

How to answer from each place: [Answer questions and approvals](../how-to/answer.md).
