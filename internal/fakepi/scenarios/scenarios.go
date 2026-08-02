package scenarios

import "time"

type StepType string

const (
	AgentStart   StepType = "agent_start"
	MessageStart StepType = "message_start"
	TextDelta    StepType = "text_delta"
	MessageEnd   StepType = "message_end"
	AgentEnd     StepType = "agent_end"
	AgentSettled StepType = "agent_settled"
	Crash        StepType = "crash"
)

type Step struct {
	Type  StepType
	Delay time.Duration
	Text  string
}

type Scenario struct {
	Name  string
	Steps []Step
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

var Scenarios = map[string]Scenario{
	Basic.Name:          Basic,
	SlowStream.Name:     SlowStream,
	CrashMidStream.Name: CrashMidStream,
}
