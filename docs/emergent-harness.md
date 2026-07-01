# The Emergent Harness

> Status: **Design note / north star.** Nothing in this document is scheduled for
> implementation. It exists to define the end goal precisely, plant terminology
> before it dilutes, and commit the project to the design disciplines that make
> the goal safe to pursue. Foundations (plugins, sessions, Inkwell, swarm
> morphologies) come first.

## 1. Motivation: "Meta-Harness" Is No Longer Enough

Codecuttle bills itself as a *meta-harness*. That term described a level: a
harness that operates on its own machinery — scaffolding new plugins
(`scaffold_plugin` → build → `reload_plugins`), recording traces of its own
execution (the Inkwell), and reconciling those traces into corrections.

The term has since been diluted. Many projects now call themselves
"meta-harnesses" because they wrap an existing agent product (Claude Code,
Codex, Cursor, etc.) and add orchestration on top. That is *harness
composition*: the wrapper inherits someone else's context construction, tool
semantics, and failure handling. It is meta in level, but it has no answer to
the question that actually matters:

> **Where do better harness configurations come from — and does the system have
> the trace data to know whether a change helped?**

"Meta" describes a level. It says nothing about a *mechanism*. This document
names the mechanism.

## 2. The Concept: Self-Organization, Not Just Self-Modification

The end goal for Codecuttle is an **emergent harness**, defined operationally:

> A harness whose behavior-shaping components (skills, routing rules, prompt
> fragments) are **grown from its own execution traces** via
> reinforcement-with-decay and outcome-gated selection, rather than exclusively
> hand-authored — within an engineered, enforced chassis.

The intellectual lineage is systems science rather than classical ML:
generative social science (Epstein & Axtell, *Growing Artificial Societies*)
and biological self-organization (Camazine et al., *Self-Organization in
Biological Systems*). Two mechanisms from that literature map directly onto
what Codecuttle has already built:

### 2.1 Stigmergy: The Inkwell Is a Trace Field

Stigmergy is coordination through traces left in a shared environment — the
ant's pheromone trail, the termite's mud pellet. The Inkwell is *literally
this*: agents act, deposit execution traces, and future behavior
(reconciliation, skill injection, eventually routing) is conditioned on the
accumulated trace field.

### 2.2 Local Activation Rules: Skills Are Stimulus-Response Pairs

Conditional skills (`on_error:compile|on_language:go`) are local activation
rules — "in the presence of stimulus X, express behavior Y" — the same grammar
that produces emergent construction in wasps and trail networks in ants. No
global controller decides what knowledge enters context; local triggers firing
against local state do.

The architecture already speaks self-organization. The emergent-harness phase
gives it dynamics: traces don't just *inform* behavior, they *reshape the rule
population itself*.

### 2.3 Terminology

| Term | What it claims | Verdict |
|------|----------------|---------|
| Meta-harness | Operates on its own machinery (a level) | True of Codecuttle today; diluted by wrappers |
| Self-evolving harness | Variation + selection + retention loop | Accurate for the mechanism, but implies more directedness than the design has |
| Self-organizing harness | Local rules + feedback through a shared trace field | The most technically precise description |
| **Emergent harness** | Capabilities appear that were never explicitly authored | **The banner term.** Falsifiable in Epstein's sense; nobody wrapping another product can claim it |

## 3. The Compartment Model: Engineered Chassis, Emergent Tissue

Emergence is a description, not a control knob. Half the lesson of the
Sugarscape work is how hard it is to get the emergence you *want* rather than
the emergence you *get*. A harness that is emergent everywhere would be
undebuggable and unsteerable.

The design therefore splits the system into two compartments:

### 3.1 The Chassis (fixed, designed, boring)

These components are hand-engineered, code-reviewed by humans, and **never**
modified by the evolution loop:

* The plugin bus (Cuttlebone substrate, gRPC lifecycle, sandboxing)
* Session persistence and compaction machinery
* The enforcement layer (tool discipline interceptor, workbench sandboxing,
  approval flow, protected-branch rules)
* Safety rails: budgets, timeouts, circuit breakers
* **The evolution loop itself** — the machinery that grows, decays, and selects
  tissue is chassis, not tissue. The system does not evolve its own selection
  criteria.

### 3.2 The Tissue (grown, decaying, selected)

These components form the *population* that the loop operates on:

* **Skills** — markdown documents with trigger expressions
* **Trigger conditions** — when a skill activates, and at what priority
* **Routing weights** — which model tier / node handles which task class
  (the Chromatophore Router's decision table)
* **Prompt fragments** — tier-conditional guidance blocks (see
  `on_model_tier` discussion in multi-model design)

Cephalopod-appropriately: the shell is fixed; the chromatophores adapt.

### 3.3 Unit of Variation: Coarse and Inspectable

Ant colonies get away with opaque dynamics because nobody code-reviews a
pheromone field. We do. The unit of heredity is therefore the **whole skill /
whole rule**, expressed as human-readable markdown or YAML, so that:

* A human (or a reviewer node) can read a diff of *what the system learned*
* Learned tissue flows through normal git review — a branch, a PR, a merge
* Nothing sub-symbolic (weights, embeddings-as-behavior) is evolved for as
  long as possible

## 4. The Four Feedback Disciplines

The known failure mode of self-improving agents is **self-reinforcing bad
habits from noisy traces** — in the ant literature, premature convergence on a
suboptimal trail. Biology's countermeasures are well understood, and each
becomes a hard design rule:

### 4.1 Evaporation

Pheromone decays unless re-deposited by ongoing success. Every learned skill,
fragment, or routing weight carries a decaying confidence score that must be
replenished by *recent* validating traces. A habit reinforced by one noisy
early trace dies of neglect. **Memory is never append-only.**

### 4.2 Quorum Thresholds

Ants require a quorum before committing to a nest site. No trace-derived
behavior is promoted into active tissue until **N independent sessions**
corroborate it. Single-trajectory noise cannot promote anything.

### 4.3 Persistent Exploration Noise

Some ants always ignore the trail. A small fraction of sessions run with a
given learned rule *disabled* — a standing control group. This is how the
system detects that a reinforced habit has stopped paying rent, rather than
riding it forever.

### 4.4 Paired Inhibition

Camazine's core observation: self-organization is never positive feedback
alone; every runaway loop is capped by an inhibitor. As a development
discipline: **every new reinforcement channel ships with its decay channel in
the same PR.** A PR that adds promotion without demotion is incomplete by
definition.

## 5. Reward: The Environment Grades Honestly

The hard part of self-evolution is the reward signal. Codecuttle's domain has
an advantage most RL-for-agents settings lack: **verifiable, environmental
outcomes.**

* Compile / build results
* Test suite pass rates
* Lint status
* PR merged vs. reverted
* Task completed without retry or human correction

Epstein's dictum was *"if you didn't grow it, you didn't explain it."* Ours is
stricter: **if it didn't make verified outcomes better, it didn't improve the
harness.**

Concretely, selection is two-staged:

1. **Growth (stigmergic):** candidate tissue is proposed from live Inkwell
   traces — a recurring error class suggests a skill; a recurring successful
   pattern suggests a fragment.
2. **Retention (selective):** candidates are retained only if they survive
   **holdout evaluation** — a fixed micro-benchmark of canned tasks
   ("recover from this compile error", "summarize this design doc *without*
   implementing it") run per model × tissue-variant. Growth proposes;
   selection disposes.

The micro-benchmark is also, quietly, the first self-evolution loop the
project will run: harness-optimizing-the-harness with a human still turning
the crank. That is the correct first step.

## 6. Prerequisites (Why Not Yet)

The emergent phase begins only when the chassis is crystallized. Concretely:

1. **Inkwell maturity** — trace schema stable; error classification reliable
   enough that promotion/demotion signals aren't noise about noise
2. **Skills tiering** — `on_model_tier` trigger dimension implemented, so
   tissue can vary per model capability (small models need scaffolding that
   degrades large ones)
3. **Holdout benchmark** — the canned-task suite and scoring harness exist and
   run per model × prompt-variant
4. **Swarm reviewer nodes** — a read-only reviewer morphology able to gate
   tissue PRs, so learned changes get non-human *and* human review
5. **Enforcement coverage** — every rule that can be mechanized has been moved
   from prompt prose into the enforcement layer, shrinking the surface the
   evolution loop can perturb

## 7. Non-Goals

* **Evolving the chassis.** The bus, the safety rails, and the evolution loop
  itself are never tissue.
* **Sub-symbolic learning.** No fine-tuning, no weight evolution, no opaque
  policies. The unit of learning stays diffable.
* **Autonomy without review.** Learned tissue merges through PRs. The octopus
  is the model, not the ant colony: distributed adaptation, **centralized
  accountability** — most of the neurons in the arms, but unmistakably one
  animal with one intent.

## 8. Relationship to Other Documents

* [architecture.md](architecture.md) — the chassis this document holds fixed
* [sessions-and-inkwell.md](sessions-and-inkwell.md) — the trace field
  (stigmergic substrate)
* [swarm-morphologies.md](swarm-morphologies.md) — reviewer nodes and
  event triggers that will gate tissue promotion
* [multi-model-design.md](multi-model-design.md) — model tiers over which
  tissue must vary and be benchmarked
