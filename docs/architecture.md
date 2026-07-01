# Architecture

## System Overview

```mermaid
graph TD
    User[User / Terminal] --> TUI[Bubble Tea TUI]
    User --> REPL[Plain REPL / One-shot]
    TUI --> Agent[Conversation Agent]
    REPL --> Agent
    Agent --> ProviderIF[Provider Interface]
    ProviderIF --> Bedrock[AWS Bedrock ConverseStream]
    ProviderIF --> Google[Google AI Gemini]
    ProviderIF --> Ollama[Ollama Local Models]
    Agent --> PluginMgr[Plugin Manager]
    Agent --> TodoMgr[Todo Manager]
    Agent --> StateDict[State Dictionary]
    Agent --> AutoPlan[Auto-Planner]
    Agent --> Inkwell[Inkwell Reconciler]
    Agent --> SkillReg[Skill Registry]
    Agent --> Sessions[Session Store]

    PluginMgr --> |gRPC over Unix socket| P1[cuttlebone-read-file]
    PluginMgr --> |gRPC over Unix socket| P2[cuttlebone-write-file]
    PluginMgr --> |gRPC over Unix socket| P3[cuttlebone-edit-file]
    PluginMgr --> |gRPC over Unix socket| P4[cuttlebone-bash-exec]
    PluginMgr --> |gRPC over Unix socket| P5[cuttlebone-grep]
    PluginMgr --> |gRPC over Unix socket| P6[cuttlebone-glob]
    PluginMgr --> |gRPC over Unix socket| P7[cuttlebone-git]
    PluginMgr --> |gRPC over Unix socket| P8[cuttlebone-github]
    PluginMgr --> |gRPC over Unix socket| P9[cuttlebone-list-directory]
    PluginMgr --> |gRPC over Unix socket| P10[cuttlebone-web-fetch]
    PluginMgr --> |gRPC over Unix socket| P11[cuttlebone-web-search]
    PluginMgr --> |gRPC over Unix socket| P12[cuttlebone-go-skills]
    PluginMgr --> |gRPC over Unix socket| P13[cuttlebone-quant]

    Bedrock --> |Stream Events| Agent
    Agent --> |Tool Results| Bedrock

    Inkwell --> |Corrective Prompts| Agent
    SkillReg --> |Conditional Knowledge| Agent
    Sessions --> |Persist/Restore| Disk[(~/.local/share/codecuttlectl/)]

    subgraph "Cuttlebone Substrate"
        P1
        P2
        P3
        P4
        P5
        P6
        P7
        P8
        P9
        P10
        P11
        P12
        P13
    end
```

## Conversation Flow

```mermaid
sequenceDiagram
    participant U as User
    participant T as TUI
    participant A as Agent
    participant B as Bedrock
    participant P as Plugin
    participant I as Inkwell
    participant S as Session Store

    U->>T: Type message + Enter
    T->>A: Submit message
    A->>A: effectiveSystemPrompt() (base + reconciler + skills)
    A->>B: ConverseStream (system + history + tools)

    loop Streaming
        B-->>A: TextDelta / ReasoningDelta
        A-->>T: StreamTextMsg / StreamReasoningMsg
        T-->>U: Render text in viewport
    end

    alt Tool Use
        B-->>A: ToolUseStart + ToolInputDelta + ToolUseStop
        A->>P: Execute(input) via gRPC
        P-->>A: ExecuteResponse(output)
        A->>I: Record InkEntry (timing, error class)
        A->>S: Flush session state
        I-->>A: Advice (corrective prompt injection)
        A->>B: Continue with ToolResult
    end

    B-->>A: MessageStop
    A->>S: Final flush
    A-->>T: StreamDoneMsg
    T-->>U: Final render, ready for input
```

## Plugin Architecture (Cuttlebone Substrate)

```mermaid
graph LR
    subgraph Host Process
        Orchestrator[codecuttlectl]
        Manager[Plugin Manager]
        SkillReg[Skill Registry]
        Orchestrator --> Manager
        Manager --> SkillReg
    end

    subgraph Plugin Processes
        P1[cuttlebone-read-file<br/>PID: separate]
        P2[cuttlebone-git<br/>PID: separate]
        P3[cuttlebone-go-skills<br/>PID: separate<br/>Knowledge only]
    end

    Manager -->|"1. Discover binaries (cuttlebone-* prefix)"| Dir[plugins/]
    Manager -->|"2. Launch subprocess"| P1
    Manager -->|"3. gRPC Handshake (10s timeout)"| P1
    Manager -->|"4. Describe() → metadata + skills"| P1
    Manager -->|"5. Execute() (per-plugin timeout)"| P1
    Manager -->|"6. Crash → auto-restart (up to 3x)"| P1

    P1 -.->|Unix domain socket| Manager
    P2 -.->|Unix domain socket| Manager
    P3 -.->|Skills registered, no Execute| SkillReg
```

## Inkwell Reconciliation Loop

```mermaid
graph TD
    ToolExec[Tool Execution] --> InkEntry[Record InkEntry]
    InkEntry --> Classify[Classify Error<br/>Go/Python/TS/Rust parser]
    Classify --> Diagnose[Diagnose Patterns<br/>loop detection, recency]
    Diagnose --> Advise[Generate Advice]
    
    Advise -->|Single Error| Correct[Corrective Prompt<br/>targeted guidance]
    Advise -->|Looping 3+| Escalate[Loop Warning<br/>force strategy change]
    Advise -->|5+ failures| Abort[Abort Recommendation<br/>stop and explain]
    
    Correct --> Inject[Inject into System Prompt]
    Escalate --> Inject
    Abort --> Inject
    Inject --> NextConverse[Next Converse Call]
```

## Skills System

```mermaid
graph TD
    Plugins[Plugin Discovery] -->|Describe().Skills| Registry[Skill Registry]
    Registry --> Evaluate{Evaluate Triggers}
    
    Context[Session Context<br/>errors, tools, files, language] --> Evaluate
    
    Evaluate -->|"always"| Active[Active Skills]
    Evaluate -->|"on_error:compile"| Active
    Evaluate -->|"on_file:*.go"| Active
    Evaluate -->|"on_loop"| Active
    
    Active --> Budget[Apply Token Budget<br/>24000 default]
    Budget --> Render[Render into System Prompt]
    Render --> Agent[Agent's effectiveSystemPrompt]
    
    Agent2[Agent] -->|"get_skill(name)"| OnDemand[On-Demand Retrieval]
    OnDemand --> Registry
```

### Trigger Expression Syntax

```
always                    — inject every call (subject to budget)
on_request                — only via get_skill tool
on_error:compile          — on specific error class
on_error:*                — on any error
on_tool:bash_exec         — when tool was used recently
on_file:*.go              — when matching file referenced
on_language:python        — when language detected
on_turn:first             — first turn only
on_loop                   — when looping detected

Combined: on_error:compile|on_language:go   (OR logic)
```

## Session Persistence

```mermaid
graph LR
    Agent[Agent Loop] -->|After each tool exec| Flush[Flush Session]
    Flush --> Serialize[MarshalHistory<br/>types.Message → JSON]
    Serialize --> AtomicWrite[Write .tmp → rename .json]
    AtomicWrite --> Disk[(~/.local/share/codecuttlectl/sessions/)]
    
    Resume[--session ID] --> Load[Load .json]
    Load --> Deserialize[UnmarshalHistory<br/>JSON → types.Message]
    Deserialize --> Agent
```

### Session File Structure

```json
{
  "meta": {
    "id": "ses_abc12345",
    "created_at": "2026-06-01T08:00:00Z",
    "updated_at": "2026-06-01T08:15:00Z",
    "title": "Fix Compile Errors",
    "model": "us.anthropic.claude-opus-4-6-v1",
    "region": "us-east-1",
    "work_dir": "/home/user/project",
    "stats": { "turns": 3, "tool_calls": 7, "input_tokens": 5000, "output_tokens": 2000 }
  },
  "messages": [...],
  "todos": [...],
  "inkwell": [...]
}
```

## Naming

The architecture draws from cephalopod biology:

| Name | Biological Analog | Software Function |
|------|-------------------|-------------------|
| **Cuttlebone Substrate** | Rigid internal shell providing compressive strength | Compiled protobuf + gRPC layer enforcing type safety |
| **Inkwell** | Ink sac defense mechanism | Diagnostic capture and error analysis cache |
| **Chromatophore Engine** | Pigment cells enabling rapid color change | (Future) Dynamic complexity routing via Chomsky Hierarchy |
| **Optic Lobe** | Multi-dimensional visual processing center | (Future) PostgreSQL + pgvector + AGE hybrid memory |
| **Stellate Ganglion** | Peripheral motor center bypassing the brain | Raw bash/shell fallback when structured tools fail |
| **Arm Nodes** | Distributed neural clusters in tentacles | (Future) Edge-localized lightweight inference agents |

## TUI Layout

```
┌─────────────────────────────────────────────────────────┐
│  codecuttlectl | model | region | 8p        in:X out:X  │ Status Bar
├─────────────────────────────────────────────────────────┤
│  ❯ user message                                         │
│                                                         │
│    ◇ thinking... (muted, collapsible)                   │ Viewport
│                                                         │ (scrollable)
│  ◆ codecuttle                                           │
│    assistant response (markdown rendered)                │
│  ⚡ Calling read_file...                                │
│  ✓ 1: package main...                                   │
├─────────────────────────────────────────────────────────┤
│  ╭──────────────────────────────────────────────────╮   │
│  │ Type a message...                                │   │ Input
│  ╰──────────────────────────────────────────────────╯   │
├─────────────────────────────────────────────────────────┤
│  2/5 done · 1 active · 2 pending        ctrl+t tasks   │ Todo Bar
├─────────────────────────────────────────────────────────┤
│  enter send  ctrl+r thinking  ctrl+t tasks  ctrl+c quit │ Help Bar
└─────────────────────────────────────────────────────────┘
```

## Data Flow: Extended Thinking

When `--thinking` is enabled:

```mermaid
sequenceDiagram
    participant B as Bedrock
    participant A as Agent/TUI

    B->>A: ContentBlockStart (reasoning)
    loop Reasoning Stream
        B->>A: ReasoningDelta (text chunk)
        Note over A: Render indented, muted
    end
    B->>A: ReasoningSignature (verification token)
    B->>A: ContentBlockStop

    B->>A: ContentBlockStart (text)
    loop Response Stream
        B->>A: TextDelta (text chunk)
        Note over A: Render as assistant response
    end
    B->>A: ContentBlockStop
    B->>A: MessageStop
```
