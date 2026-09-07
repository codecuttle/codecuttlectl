# Runtime characterization ledger

## Status and purpose

This ledger began with the R1 test/documentation slice, measured against
merged main `4bcf563` (#67). Subsequent repair entries below distinguish fixed
Engine contracts from still-unmigrated frontend behavior.

## R2: Engine ownership and lifecycle

`Engine.Submit` admits one turn at a time and returns a handle with `ID`, `Events`,
`Done`, `Wait` and `Cancel`. Busy/closed/pre-cancelled submissions leave Agent
history untouched. IDs are monotonic within an Engine, not durable global IDs.
`Wait` timeout only cancels that wait; use `Cancel` to stop execution. `Close`
permanently rejects new submissions, requests cancellation and waits for the
actual worker; a timed-out close does not falsely free an uncooperative worker.
Provider/plugin resources are still owned by the caller, not closed by Engine.

Snapshots are deep-copied **last-completed checkpoints** of history, todos,
workbench, provider/node and prompt, plus current admission/closed state. They do
not expose live partial history, Inkwell/audit detail, usage, or disk durability.
No Engine lock is held across provider/tool I/O or approval callbacks. Cancelled
queued tool calls are returned as nonexecuted errors; completed side effects are
not rolled back. Legacy blocking approvals must return before Close can finish.

The event mailbox is bounded at 128 entries, with two reserved for plan/terminal
notifications. Overflow cancels with `ErrSlowConsumer` instead of silently losing
a turn or blocking finalization. The handle always retains the final outcome;
there is exactly one terminal event. Full typed event coverage/coalescing and
state resynchronization remain R3—not implemented merely by lifecycle APIs.

**Integration boundary:** current CLI/TUI still use Agent/direct provider paths.
The new serialization guarantee applies only to `Submit`/`StreamTurnAsync` on one
Engine with exclusive Agent access. Do not retain/mutate `Engine.Agent()` while
submissions run, or wrap the same Agent in multiple Engines. That deprecated
compatibility escape hatch stays until R6/R8; this PR is not the TUI cutover.
Cancellation guard checks in Agent also benefit existing streaming callers.


**A green characterization test is not proof of correct behavior.** Tests named
`TestKnownDivergence_*` assert a measured defect so changes cannot silently alter
it. In its repair PR, replace that assertion with the desired invariant, observe
it fail on the parent, implement the fix, and update this ledger. Never retain a
bug merely to satisfy its characterization test, or hide the test with Skip.

## Evidence and repair sequence

R2 = Engine turn ownership; R3 = events; R4 = tool dispatcher/approval;
R5 = request state/handoff/diagnostics; R6 = persistence/CLI; R7 = async bridge;
R8 = TUI cutover; R9 = provider/legacy loop consolidation.

| ID | Observed evidence | Desired contract / next slice |
| --- | --- | --- |
| C01 | `runtime-characterization.py`: fresh TUI/REPL/one-shot each makes one primary request and displays the text answer | Preserve success across modes; exact save ordering is not measured here (R6) |
| C02 | Same runner: two calls (success and failure), correction, final answer; all modes make three requests, preserve ordered tool/result IDs and failure diagnostics, and append exactly `AB` to a disposable file | Preserve pairing and one side effect per invocation (R4/R8); Agent structured error/Inkwell/audit coverage reused from #66 |
| C03 | First outgoing requests contain bash plus five native tools in REPL/one-shot, but bash only in TUI; package TUI test also records native omission without plugins | One authorized schema catalog shared with execution (R4/R8) |
| C04 | `TestKnownDivergence_TUICatalogAndTodoAuthorization`: restricted Agent denies todo, but TUI special handling accepts the forged call and mutates its own todo list | Native calls cannot bypass workbench authorization; one todo owner (R4/R8) |
| C05 | `TestKnownDivergence_TUIApprovalDeniesAgainAndLosesEarlierResults`: TUI approve still returns Agent approval denial; inert plugin is never invoked for gated call; Agent callback control invokes it once | Approve once executes once; deny executes zero times (R4/R8) |
| C06 | Same test: first tool really executes and appears in ApprovalRequest.CompletedResults, but after approve **or deny** the ContinueStream results contain only the later gated call | Preserve earlier results once across approval pauses (R4/R8); newly reproduced gap |
| C07 | `TestKnownDivergence_HandoffLeavesTUIOnSourceProvider`: Agent node/persona changes to target, but next actual TUI provider call uses source and old persona | Transactional handoff and one state authority (R5/R8); in-process recording providers here, not live endpoints |
| C08 | `TestKnownDivergence_PrimaryProfileNotApplied`: named primary identity applies but its persona/workbench do not; `TestKnownDivergence_HandoffWithoutPoolPanics` catches a nil-pool panic in-process | Validate prerequisites and apply primary profile atomically; no panic on invalid handoff (R5) |
| C09 | Real binaries with max-steps=1: Agent modes issue one request then limit error; TUI issues a second request and completes | Shared logical-round limit; retries remain inside a round (R5/R8) |
| C10 | #66 built-plugin Agent test now records both request system strings: advice is generated by Inkwell but absent from next system prompt; binary runner confirms absence in all three modes | Corrective advice must reach model, not just logs (R5) |
| C11 | Repaired in R2: `TestEngineEmitsOneTerminalError` failed with two terminals before correction; handle/event agreement and undrained full-mailbox completion now tested | One terminal outcome delivered; complete event vocabulary/coalescing remain R3 |
| C12 | Repaired in R2: 32 concurrent submissions yield one winner/31 busy errors; cancelled worker holds admission until it stops; history unchanged for rejections | `engine_lifecycle_test.go` tests real Agent with barrier provider, including snapshot readers and shutdown |
| C13 | Existing `TestRateLimitCancelAndStaleEvents` retained | Preserve #67 cancel/generation behavior through R2/R3/R8 |
| C14 | Not newly exercised: store write ordering, auxiliary titles racing saves, draft/resume parity | Required before R6/R8; do not infer from temporary session creation |
| C15 | Not newly exercised: async foreground/task completion interaction | Required before R7/R8; never drop TUI-owned scheduling on cutover |
| C16 | Existing HTTP/SSE 429 recovery/exhaustion/cancel fixtures and terminal scenarios retained | No repeated tools or retry-budget multiplication |
| C17 | Not newly exercised: full thinking/config/reload-dir propagation and Bedrock capabilities | Required before R5/R6/R9 |

### Exact observed binary counts

| Scenario | TUI requests | REPL requests | One-shot requests |
| --- | ---: | ---: | ---: |
| Text | 1 | 1 | 1 |
| Two calls, failure, correction, final | 3 | 3 | 3 |
| Same first tool round, max-steps=1 | **2** | 1 | 1 |

These counts exclude auxiliary title requests, detected by their specific prompt,
not by streaming mode (one-shot primary inference is non-streaming too). The
one-shot limit returns exit 1; REPL prints the error and exits 0 after `exit`.

## Validation architecture and safety

- `scripts/runtime-characterization.py` builds one temporary CLI and bash plugin
  and runs nine isolated scenarios: 3 modes × 3 scenarios. Each scenario has its
  own session/workspace roots and a finite local server request budget.
- HTTP/SSE handlers record independently decoded request snapshots. Assertions
  retain schema names, tool-use/result pairing, diagnostics and actual side
  effects rather than comparing terminal styling or model claims.
- TUI tests exercise `executePendingTools`, approval Update/continuation and real
  plugin RPC execution. They do not run the whole terminal approval UI; that is a
  required future R4/R8 regression, not claimed here.
- `testdata/runtime-plugin` is an **inert test-only** plugin named `git` to trigger
  existing approval classification. It never executes git, shell commands or
  destructive actions; it appends input to a temporary invocation log. It is built
  explicitly by the test and is not part of installed plugin targets.
- The only executed shell commands append markers to temporary files, print
  synthetic errors and exit. No cloud calls or credentials, real git operations,
  user sessions or installed binaries are used. Live data is not a fixture.
- Recording provider JSON is a snapshot, not an alias of mutable Agent history.
  Tests are serial unless synchronization explicitly makes concurrency safe.
- Finite subprocess/provider budgets and cleanup prevent expected defects from
  producing an unbounded inference loop. No concurrent mutation race is added to
  make the suite fail intentionally.

## Commands and completion criteria

```text
make test-runtime-characterization
make validate
go test -race -count=5 ./internal/conversation ./internal/tui
```

`make validate` retains the six earlier terminal scenarios and adds the nine mode
characterizations. Go package tests include the inert plugin control and known
runtime divergences. Use `go test -run TestKnownDivergence` for fast targeted
inspection (plugin construction still occurs in the approval test).

R1 is complete when the observed ledger and test foundation pass; parity is not
fixed. Remaining R1 measurement gaps above are explicit. R2 now tests concurrent
admission, undrained mailbox completion, close/cancel behavior and copied
snapshots. R3 still needs complete event coverage and resynchronization;
R6–R9 cannot cut over functionality on the strength of lifecycle tests alone.

## Repair PR discipline

1. Start from merged main and keep one behavior-changing slice under review.
2. Convert the relevant known-defect assertion to the desired contract before
   implementing the fix; keep unrelated characterization tests unchanged.
3. Add the affected real-binary scenario (especially approval and handoff).
4. Preserve #65–#67 regressions, record exact validation scope, and link the repair
   PR here. No silent snapshot replacement or skipped failures.
5. Do not connect the TUI to today's Engine until turn ownership, typed events,
   shared approval/dispatch, persistence and async compatibility are ready.
