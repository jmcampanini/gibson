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

var Scenarios = map[string]Scenario{
	Basic.Name: Basic,
}
