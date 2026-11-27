# What crowsnest protects, and what it does not

A web terminal is a shell as you, reachable from a browser. crowsnest is built to fail closed,
and to be honest about where its protection ends. In plain words:

## One user

crowsnest has **one login**, and whoever has it has a shell as the user crowsnest runs as. There
are no roles, no read-only users, no sharing. Treat the password like your SSH key.

- **Before setup** it serves nothing but a setup page that needs a one-time token from its log.
- **The login**: a stored password is hashed with argon2id; five wrong tries a minute per client,
  and a global cap. Sessions are cookies that only this host and port accept.
- **Every request** must name a host crowsnest knows (this stops DNS-rebinding attacks), and every
  change must come from crowsnest's own page (Origin and a custom header: a malicious site you
  visit cannot drive it).
- **By default it listens only on localhost.** `--no-auth` is refused on an open address unless
  you add `--i-know-this-is-open`. The documented way to reach it from a phone is your tailnet,
  with `tailscale serve`.
- **Secrets** (the password, the ElevenLabs key, the notify URL) are never logged, never returned
  by the API, and never passed to agents' environments.

## Agents share your user

Every agent runs as the same user as crowsnest. Processes of one user are not isolated from each
other: any agent can read another agent's environment and files, and so act as that agent towards
crowsnest (finish its job, silence its supervision, ask questions as it). crowsnest's per-agent
tokens stop **accidents** (a confused agent naming the wrong job), not a **hostile** process. The
only hard boundary would be running agents as a different user (the devbox container is a step in
that direction: agents reach only what is mounted into it).

So: give agents what you would give a capable colleague with your laptop, and no more. Do not put
production credentials where agents can read them.

## Protected branches and hosts

You can name **protected branches** (`--protected-branch`, Settings → Guard) and **protected
hosts** (`--protected-host`, the protected-hosts file). Then:

- the **guard** refuses, before it runs, any Bash command that would commit or push onto a
  protected branch, force-push it, or reach a protected host. It also refuses stopping the herdr
  server or driving another herdr session. The agent is told to ask you instead;
- an **approval** naming a protected host is **dangerous** (a second confirmation, never from a
  notification, never by a rule);
- **Land** never merges into or pushes onto a protected branch; it offers a pull request.

**The guard is a seatbelt, not a boundary.** It stops an agent that means well from making a
mistake. An agent determined to get past it (writing its own script, using another tool) can, as
it could get past anything else running as your user. Real protection for production is not
giving agents the credentials.

## What the human gates mean

Some decisions are reserved for you, and crowsnest makes sure an agent cannot take them by itself:

- **Landing**: an agent can only *ask* to land; the request is pinned to a commit and only your
  plain Allow lands it. What lands is the commit you reviewed, or nothing.
- **Wider work**: a job an agent asks for outside its own directory, with another engine, or
  without isolation waits for your Allow.
- **Task files that start jobs or poke leads**: run only after you approve their exact text. Any
  edit asks again; a restore forgets every approval.
- **Gates are never answered from a notification**: no buttons, and the server refuses one. An
  agent's question cannot pretend to be a gate (crowsnest's gate markers are refused in agent
  questions).
- **Approvals**: allow once only. No browser action grants a standing permission (that stays in
  the terminal, or in a rule you write). Anything that goes wrong falls back to Claude's own
  dialog, never to an allow.
- **Expiry is never consent**: an unanswered question or approval decides nothing.

These hold against a prompt-injected agent working through crowsnest's own interfaces. They do not
hold against a process that already acts as your user by other means (see above).

## What crowsnest does not do

- It does not edit your repositories' `CLAUDE.md`, `AGENTS.md`, rules, skills or Claude settings.
- No repository hook runs when crowsnest lands a job.
- It never runs a state-changing Tailscale command; it prints them.
- It never answers a terminal dialog by itself.
- It never retypes an answer by itself.

## Residual risks, stated plainly

- **Plain HTTP on a shared host name**: browsers send cookies to every port on a host, so another
  web service you open on the same name (a dev server on `localhost:5173`) can receive your session
  cookie. Give crowsnest a name of its own (`tailscale serve` with HTTPS) and open nothing else
  under it.
- **The setup window**: until setup is done, whoever has the token from the log becomes the owner.
  Finish setup promptly; on a shared machine start with `--open never`.
- **A backup from somewhere else** carries its own login, roles, task files and risk rules; treat
  it like a script.

The full threat model is section 4 of `DESIGN.md` in the repository.

[Open Settings → Protected hosts and branches](app:/settings#guard) to set them.
