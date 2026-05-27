# Oompa Presentation — Google Slides Content

## Setup instructions

1. Create a new Google Slides deck
2. Go to Slide > Edit theme > pick a dark background or set custom color `#151515`
3. In Slide > Edit theme > Colors, set accent color to `#EE0000`
4. Fonts: use "Red Hat Display" for titles (available in Google Fonts dropdown), "Red Hat Text" for body text, "Red Hat Mono" for code
5. Paste each slide below as a new slide
6. Speaker notes go in the notes panel at the bottom of each slide

---

## Slide 1 — Title

**Layout:** Title slide (centered)

### Title
Oompa

### Subtitle
How I built an autonomous code maintenance agent
— and why I had to turn it down

### Bottom
Enrique Llorente | Red Hat

### Speaker notes
30 minute talk. Honest, personal, not a sales pitch. This is about my workflow, my mistakes, and what I learned.

---

## Slide 2 — Section divider

**Layout:** Section header (centered)

### Number (large, red, faded)
01

### Title
The question

---

## Slide 3 — The pain

### Title
The pain

### Bullets
- I maintain PRs across **6+ repositories**
- Rebases pile up. CI failures go stale. Reviews rot.
- By the time I get to a PR, it needs rebasing again
- Flaky tests block merges for days — nobody tracks them

### Speaker notes
Start with the real problem. Don't oversell it, just state facts. Everyone in the room has felt this.

---

## Slide 4 — The inspiration

### Title
The inspiration

### Bullets
- I saw projects like **OpenClaw** — 53k commits, AGENTS.md baked in, AI agents as first-class contributors
- I wanted to explore a question:

### Highlight box (red border, centered)
Can a software project maintain itself?
How far can we push that today with LLMs?

### Footer text (gray, small)
That curiosity, plus the real maintenance pain, led me to build **oompa**.

### Speaker notes
OpenClaw by Peter Steinberger. 375k stars. AI-maintained. I was inspired by the idea, not trying to replicate it. I wanted to see what "autonomous maintenance" looks like in practice.

---

## Slide 5 — Section divider

**Layout:** Section header (centered)

### Number (large, red, faded)
02

### Title
How it works

---

## Slide 6 — The loop

### Title
The loop

### Flow diagram (4 boxes with arrows between them)

[ Poll ] → [ Analyze ] → [ Act ] → [ Report ]
  GitHub API    CI logs, reviews    fix, rebase, reply    Slack, comments

### Bullets
- **Single Go binary**, YAML config, one goroutine per role
- Runs as a **systemd user service** on a VM
- **Auto-deploys** on push to main — CI builds a release, oompa detects the new version, restarts itself

### Speaker notes
Keep this brief. One slide, move on. The architecture isn't the interesting part — what it does and what went wrong is.

---

## Slide 7 — The config (the dial)

### Title
The config — *the dial*

### Code block (monospace, syntax highlighted)
```yaml
projects:
  - owner: ovn-org
    repo: ovn-kubernetes
    roles: [prs]
    reactions: []              # report-only — just watch

  - owner: nmstate
    repo: kubernetes-nmstate
    roles: [prs, issues, triage]
    reactions: [ci, rebase, review]

  - owner: qinqon
    repo: oompa
    roles: [prs, issues]
    reactions: [ci, rebase, review]
    skip-checks: [can-be-merged]
```

### Footer text (gray, small)
Each project gets its own level of automation.
`reactions: []` means oompa watches but does nothing.
This dial exists because I learned I needed it.

### Speaker notes
Show the real config. Point out reactions: [] vs full reactions. "This dial exists because I learned I needed it" — foreshadow section 4.

---

## Slide 8 — Section divider

**Layout:** Section header (centered)

### Number (large, red, faded)
03

### Title
What I do with it

### Subtitle (gray)
Live examples from real repos

---

## Slide 9 — ovn-kubernetes

### Title
ovn-kubernetes — *the one where I went too far*

### PR card (left-bordered box)
**ovn-org/ovn-kubernetes**
**#6229** — kubevirt live migration cleanup
Someone else's PR. Oompa started commenting without anyone asking.

### Bullets
- **~15 CI analysis comments** — each analyzing a different failure
- **5 flaky issues created** (#6297, #6312, #6321, #6337, #6338)
- Duplicate detection — *"This appears to be a duplicate of #6141"*
- Deep root cause analysis — FRR SIGSEGV, KIND CDN corruption, Koji 404, closure bugs

### Warning text (orange)
**All of this on someone else's PR, on a project where nobody asked for it.**

### Speaker notes
OPEN: https://github.com/ovn-org/ovn-kubernetes/pull/6229
Scroll through the comments. Show the depth of analysis. Then: "Nobody asked for this. This is where I crossed the line."

---

## Slide 10 — hypershift

### Title
hypershift — *where the cost incident started*

### PR card (left-bordered box)
**openshift/hypershift**
**#8365** — Generate KubeVirt nmstate network config conditionally
My PR. Oompa went full autonomous.

### Bullets
- CI fix pushed — *Lint failure, pushed a fix*
- Infrastructure failure detection — *RPM mirror HTTP 404*
- Flaky issue creation — *#8376 for codecov/project*
- Review response — *"Done. Added nil guards to all three exported helpers"*
- Retests — `/test e2e-kubevirt-aws-ovn`

### Warning text (orange)
**This is also where the review retry loop burned ~$2-4k in 5 days.**

### Speaker notes
OPEN: https://github.com/openshift/hypershift/pull/8365
Show the review comment reply. Show the CI analysis. Then transition: "And this is also where oompa got stuck in a loop trying to address the same review over and over."

---

## Slide 11 — kubernetes-nmstate

### Title
kubernetes-nmstate — *the right way*

### PR card (left-bordered box)
**nmstate/kubernetes-nmstate**
**#1467** — eliminate API server dependency at startup for TLS config
My own PR, my own project. This is the legitimate use case.

### Bullets
- **Human reviewer** (mkowalski) + bot reviewer feedback addressed
- *"Good point. Fixed in 27617adc9"*
- *"Makes sense. Extracted to a `tlsProfileConfigPath` constant"*
- **5 CI checks fixed** at once — docs, unit tests, e2e, upgrade, operator
- Rebase with conflict resolution
- Structured CI analysis with root cause and recommendation
- **Merged**

### Footer text (green)
Oompa keeps my PRs alive so I can focus on the next thing.

### Speaker notes
OPEN: https://github.com/nmstate/kubernetes-nmstate/pull/1467
Show the review thread — oompa addressing mkowalski's feedback. Show the CI comments. This is oompa working FOR me, not on behalf of me. "This is the right use case. My PR, my project."

---

## Slide 12 — oompa (self-maintenance)

### Title
oompa — *it maintains itself*

### PR card (left-bordered box)
**qinqon/oompa**
**#193** — fix: triage dedup matches on job name only
The full self-maintenance loop.

### Bullets
- I create an issue with `good-for-ai` label
- Oompa picks it up, creates a PR
- **gemini-code-assist** and **coderabbitai** leave review comments
- Oompa responds inline:

### Sub-bullets (indented, colored)
- (green) **"Fixed."** — accepts feedback, pushes rune-aware truncation fix
- (orange) **"Declining this one."** — *"The exact same `body[:500]` pattern exists in the pre-existing code..."*
- (cyan) **Reviewer acknowledges:** *"@qinqon, thanks for the thorough update"*

### Speaker notes
OPEN: https://github.com/qinqon/oompa/pull/193
Show the review thread. Point out the "Declining" reply — it reasons about WHY. "I create the issue. Oompa does the rest. Picks it up, writes the fix, addresses reviewers, merges. Then auto-deploys. I don't touch it."

---

## Slide 13 — Section divider

**Layout:** Section header (centered)

### Number (large, red, faded)
04

### Title
What went wrong

---

## Slide 14 — The social mistake

### Title
The social mistake

### Bullets (use fragments/animations — reveal one at a time)
- **I got excited.** AI can do this! Let me turn everything on everywhere.
- **I didn't ask.** Deploying an agent on a project is a **policy decision**. It affects everyone who contributes. I treated it as a technical problem.
- **It eroded trust.** When people see bot comments they didn't expect, bot reviews they didn't ask for — it doesn't matter how good the analysis is.
- **I entered AI psychosis.** The excitement is real, the hype is contagious, but the damage to your reputation and community standing is also real.

### Speaker notes
This is the most important slide. Pause between points. Be honest. Don't soften it. The audience will respect it.

---

## Slide 15 — Trust

### Title
Doing it wrong erodes trust — *with reason*

### Highlight box (red border, centered, large text)
Agent integration is a **project policy**, not a technical decision.
It's not your call to make alone.

### Bullets
- You wouldn't add a new CI check to someone's repo without asking
- You wouldn't merge changes to their test suite without asking
- An AI agent commenting on their PRs is the same thing
- The community's confidence in you is not infinite

### Speaker notes
Draw the parallel to CI checks and test changes. "Would you add a new Prow job to someone's repo without asking? No? Then why would you deploy a bot that comments on their PRs?"

---

## Slide 16 — Technical mistakes

### Title
The technical mistakes

### Two cards side by side

**Card 1 (orange border):**
**$2-4k review retry loop**
Oompa got stuck trying to address the same review comment over and over on hypershift. 5 days. No cost guard.
**Fix:** retry counter, no-op detection, cost guard per PR.

**Card 2 (orange border):**
**Wrong issue dedup**
CRI-O mirror 404 matched a VLAN test failure issue — because dedup only checked the job name, not the failure content.
**Fix:** content-aware matching with failure signatures. *(PR #193 — the self-fix you just saw)*

### Footer text (gray, small)
Every failure taught me something. Every guardrail was added after something broke.

### Speaker notes
Keep this quick — 2 minutes max. The social mistake was the bigger lesson. "And on top of the social mistake, the code had bugs too." Point out that PR #193 (the self-fix) is oompa fixing its own dedup bug.

---

## Slide 17 — Section divider

**Layout:** Section header (centered)

### Number (large, red, faded)
05

### Title
Where I am now

---

## Slide 18 — The dial

### Title
The dial

### Horizontal bar visualization (4 segments, left to right, increasing intensity)

| Report only | Triage + Rebase | CI + Reviews | Self-maintaining |
|-------------|-----------------|--------------|------------------|
| ovn-kubernetes | kubernetes-nmstate | virt-tests | oompa |

(Use color gradient: gray → cyan → gold → red)

### Bullets
- **Report-only** where I don't have permission or trust yet
- **Full automation** only on my own project
- The goal isn't "more automation everywhere"
- It's **the right amount, with consent**

### Speaker notes
Point at the dial. "ovn-k is report-only because I moved it there AFTER I realized nobody asked for it. hypershift reviews are disabled because of the cost incident. The only project with full autonomy is oompa itself."

---

## Slide 19 — Being pragmatic

### Title
Being pragmatic

### Bullets (use fragments/animations — reveal one at a time)
- Slack reports give me visibility without touching anyone's PRs
- I ask before enabling actions on a project
- The tool has a dial **because I learned I needed one**
- Don't enter AI psychosis. Be pragmatic.

### Highlight box (red border, centered, large text)
The interesting question isn't *"how much can we automate?"*
It's **"how much should we, and who decides?"**

### Speaker notes
SHOW SLACK: Open your Slack channel and show a real oompa report. "This is what report-only looks like. I get visibility. Nobody's PR gets touched." End with the quote. Let it land.

---

## Slide 20 — Section divider

**Layout:** Section header (centered)

### Number (large, red, faded)
06

### Title
Live demo

### Subtitle (gray)
The factory floor

---

## Slide 21 — What you're about to see

### Title
What you're about to see

### Bullets
- The **TUI** — real-time view of every oompa working
- Each project gets a **super box** with its oompas inside
- Animated sprites — ladder-climbing (rebasing), hammer-swinging (working), sleeping (Zzz), magnifying glass (reviewing)
- **Slack notifications** from the live service
- `oompa status --events` — filtered event stream

### Footer text (gray, centered)
The oompas on screen are the ones I have permission to run.

### Speaker notes
SSH into the machine. Run the TUI. Walk through each project box. Point out which oompas are sleeping vs working. Open Slack, show a real report. Run: oompa status --events --project=nmstate. Take questions while pointing at the screen.

---

## Slide 22 — Thanks

**Layout:** Title slide (centered)

### Title
Thanks

### Links (gray, small)
**oompa** — github.com/qinqon/oompa
**OpenClaw** (inspiration) — github.com/openclaw/openclaw

### Bottom
Enrique Llorente | Red Hat

---

## Browser tabs to have ready (in order)

1. https://github.com/ovn-org/ovn-kubernetes/pull/6229
2. https://github.com/openshift/hypershift/pull/8365
3. https://github.com/nmstate/kubernetes-nmstate/pull/1467
4. https://github.com/qinqon/oompa/pull/193
5. Your Slack channel with oompa reports
6. SSH terminal ready to launch TUI

## Demo checklist

- [ ] SSH session to oompa VM ready
- [ ] TUI launches cleanly (`oompa tui`)
- [ ] Slack channel open with recent reports
- [ ] `oompa status --events` works
- [ ] All 4 GitHub PR tabs loaded
- [ ] Presenter mode / dual screen tested
