package scenarios

import "time"

type StepType string

const (
	AgentStart      StepType = "agent_start"
	MessageStart    StepType = "message_start"
	TextDelta       StepType = "text_delta"
	MessageEnd      StepType = "message_end"
	AgentEnd        StepType = "agent_end"
	AgentSettled    StepType = "agent_settled"
	ToolStart       StepType = "tool_start"
	ToolUpdate      StepType = "tool_update"
	ToolEnd         StepType = "tool_end"
	Notify          StepType = "notify"
	ExtensionError  StepType = "extension_error"
	AppendHugeEntry StepType = "huge_entry"
	ConfirmDialog   StepType = "dialog_confirm"
	Crash           StepType = "crash"
)

type Step struct {
	Type           StepType
	Delay          time.Duration
	Text           string
	ID             string
	CRLF           bool
	LiteralUnicode bool
}

type Scenario struct {
	Name                       string
	Steps                      []Step
	DelayPromptUntilUIResponse bool
}

var Basic = Scenario{
	Name: "basic",
	Steps: []Step{
		{Type: AgentStart},
		{Type: MessageStart},
		{Type: TextDelta, Text: "Hello"},
		{Type: TextDelta, Text: " from"},
		{Type: TextDelta, Text: " fake pi."},
		{Type: MessageEnd},
		{Type: AgentEnd},
		{Type: AgentSettled},
	},
}

var SlowStream = Scenario{
	Name: "slow_stream",
	Steps: []Step{
		{Type: AgentStart},
		{Type: MessageStart},
		{Type: TextDelta, Text: "Slow"},
		{Type: TextDelta, Delay: 150 * time.Millisecond, Text: " stream"},
		{Type: TextDelta, Delay: 150 * time.Millisecond, Text: " from"},
		{Type: TextDelta, Delay: 150 * time.Millisecond, Text: " fake"},
		{Type: TextDelta, Delay: 150 * time.Millisecond, Text: " pi"},
		{Type: TextDelta, Delay: 150 * time.Millisecond, Text: " keeps"},
		{Type: TextDelta, Delay: 150 * time.Millisecond, Text: " producing"},
		{Type: TextDelta, Delay: 150 * time.Millisecond, Text: " deterministic"},
		{Type: TextDelta, Delay: 150 * time.Millisecond, Text: " deltas."},
		{Type: MessageEnd},
		{Type: AgentEnd},
		{Type: AgentSettled},
	},
}

var CrashMidStream = Scenario{
	Name: "crash_mid_stream",
	Steps: []Step{
		{Type: AgentStart},
		{Type: MessageStart},
		{Type: TextDelta, Text: "Partial output before crash."},
		{Type: Crash},
	},
}

var HugeEntry = Scenario{
	Name:  "huge_entry",
	Steps: hugeEntrySteps(),
}

var DialogConfirm = Scenario{
	Name:                       "dialog_confirm",
	DelayPromptUntilUIResponse: true,
	Steps: []Step{
		{Type: AgentStart},
		{Type: MessageStart},
		{Type: ConfirmDialog, ID: "fp-d-1", Text: "Continue the fake run?"},
		{Type: TextDelta, Text: "Dialog confirmed."},
		{Type: MessageEnd},
		{Type: AgentEnd},
		{Type: AgentSettled},
	},
}

func hugeEntrySteps() []Step {
	steps := []Step{
		{Type: AgentStart},
		{Type: MessageStart},
		{Type: ToolStart, CRLF: true},
	}
	for range 1024 {
		steps = append(steps, Step{Type: ToolUpdate})
	}
	return append(steps,
		Step{Type: ToolEnd, CRLF: true},
		Step{Type: Notify, Text: "hostile record notification"},
		Step{Type: ExtensionError, Text: "deterministic extension failure"},
		Step{Type: AppendHugeEntry},
		Step{Type: TextDelta, Text: "Unicode: left\u2028middle\u2029right.", CRLF: true, LiteralUnicode: true},
		Step{Type: MessageEnd},
		Step{Type: AgentEnd},
		Step{Type: AgentSettled},
	)
}

var Scenarios = map[string]Scenario{
	Basic.Name:          Basic,
	SlowStream.Name:     SlowStream,
	CrashMidStream.Name: CrashMidStream,
	HugeEntry.Name:      HugeEntry,
	DialogConfirm.Name:  DialogConfirm,
}
