# Architecture

## System Overview

```mermaid
graph TD
    User[User / Terminal] --> TUI[Bubble Tea TUI]
    TUI --> Agent[Conversation Agent]
    Agent --> Bedrock[AWS Bedrock ConverseStream]
    Agent --> PluginMgr[Plugin Manager]
    Agent --> TodoMgr[Todo Manager]

    PluginMgr --> |gRPC over Unix socket| P1[cuttlebone-read-file]
    PluginMgr --> |gRPC over Unix socket| P2[cuttlebone-write-file]
    PluginMgr --> |gRPC over Unix socket| P3[cuttlebone-list-directory]
    PluginMgr --> |gRPC over Unix socket| P4[cuttlebone-bash-exec]

    Bedrock --> |Stream Events| Agent
    Agent --> |Tool Results| Bedrock

    subgraph "Cuttlebone Substrate"
        P1
        P2
        P3
        P4
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

    U->>T: Type message + Enter
    T->>A: Submit message
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
        A->>B: Continue with ToolResult
    end

    B-->>A: MessageStop
    A-->>T: StreamDoneMsg
    T-->>U: Final render, ready for input
```

## Plugin Architecture (Cuttlebone Substrate)

```mermaid
graph LR
    subgraph Host Process
        Orchestrator[codecuttlectl]
        Manager[Plugin Manager]
        Orchestrator --> Manager
    end

    subgraph Plugin Processes
        P1[cuttlebone-read-file<br/>PID: separate]
        P2[cuttlebone-write-file<br/>PID: separate]
        P3[cuttlebone-bash-exec<br/>PID: separate]
    end

    Manager -->|"1. Discover binaries"| Dir[plugins/]
    Manager -->|"2. Launch subprocess"| P1
    Manager -->|"3. gRPC Handshake"| P1
    Manager -->|"4. Describe() RPC"| P1
    Manager -->|"5. Execute() RPC"| P1

    P1 -.->|Unix domain socket| Manager
    P2 -.->|Unix domain socket| Manager
    P3 -.->|Unix domain socket| Manager
```

## Naming

The architecture draws from cephalopod biology. The one subsystem that's implemented and named today:

- **Cuttlebone Substrate** — The rigid internal shell of a cuttlefish provides compressive strength to an otherwise soft-bodied organism. In the software, the Cuttlebone Substrate is the compiled protobuf + gRPC layer that enforces strict type safety between the orchestrator and its tool plugins, preventing the LLM from invoking malformed tool calls.

## TUI Layout

```
┌─────────────────────────────────────────────────────────┐
│  codecuttlectl | model | region | plugins     in:X out:X│ Status Bar
├─────────────────────────────────────────────────────────┤
│  ▶ user message (highlighted background)                │
│                                                         │
│    💭 thinking... (muted, indented)                     │ Viewport
│                                                         │ (scrollable)
│  ◆ assistant response                                   │
│  ⚡ Calling tool_name...                                │
│  ✓ tool result                                          │
├─────────────────────────────────────────────────────────┤
│  ╭──────────────────────────────────────────────────╮   │
│  │ input area                                       │   │ Input
│  ╰──────────────────────────────────────────────────╯   │
├─────────────────────────────────────────────────────────┤
│  2/5 done · 1 active · 2 pending          ctrl+t expand│ Todo Bar
├─────────────────────────────────────────────────────────┤
│  ↑↓ scroll  enter send  ctrl+r thinking  ctrl+c quit   │ Help Bar
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

The reasoning signature is stored for multi-turn continuity — Bedrock requires it when sending previous reasoning context back.
