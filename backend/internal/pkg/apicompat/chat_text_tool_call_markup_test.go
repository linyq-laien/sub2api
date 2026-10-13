package apicompat

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MiMo 在下发结构化 tool_calls 的同时把同一次调用以原生标记回显进 content，
// 以下样本取自真实上游流。
const (
	mimoEchoedToolCall      = "<tool_call><function=exec>const r = await tools.exec_command({\"cmd\":\"ls\"});\ntext(r.output);</parameter></function></tool_call>"
	mimoEchoedParamToolCall = "<tool_call><function=exec><parameter=const r = await tools.exec_command({\"cmd\":\"grep -c '<svg' a.html\"});\ntext(r.output);</parameter></function></tool_call>"
)

func pushInChunks(f *chatToolCallMarkupFilter, text string, size int) string {
	var out strings.Builder
	for len(text) > 0 {
		n := size
		if n > len(text) {
			n = len(text)
		}
		out.WriteString(f.Push(text[:n]))
		text = text[n:]
	}
	out.WriteString(f.Flush())
	return out.String()
}

func TestChatToolCallMarkupFilter(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain text untouched", "我会先查看工作区。", "我会先查看工作区。"},
		{"only echoed block", mimoEchoedToolCall, ""},
		{"prose then block", "I'll grep for both markers." + mimoEchoedParamToolCall, "I'll grep for both markers."},
		{"block between prose", "before " + mimoEchoedToolCall + " after", "before  after"},
		{"two blocks", mimoEchoedToolCall + "mid" + mimoEchoedParamToolCall, "mid"},
		{"unclosed block kept", "note: <tool_call> is a tag name", "note: <tool_call> is a tag name"},
		{"lookalike prefix kept", "a <tool_ca b <t", "a <tool_ca b <t"},
		{"html in prose kept", "use <svg> and <path/>", "use <svg> and <path/>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, StripChatToolCallMarkup(tc.in))
			for _, size := range []int{1, 2, 3, 5, 7, 11} {
				var f chatToolCallMarkupFilter
				assert.Equal(t, tc.want, pushInChunks(&f, tc.in, size), "chunk size %d", size)
			}
		})
	}
}

func TestChatCompletionsChunkToResponsesEvents_StripsEchoedToolCallMarkup(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("gpt-6-luna")
	state.CustomTools = map[string]bool{"exec": true}
	state.StripToolCallMarkup = true

	idx := 0
	text := func(s string) *ChatCompletionsChunk {
		return &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{Content: &s}}}}
	}
	call := func(tc ChatToolCall) *ChatCompletionsChunk {
		return &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{tc}}}}}
	}
	// 真实顺序之一：正文前言 → 结构化调用 → 回显标记被切成多段到达。
	chunks := []*ChatCompletionsChunk{
		text("I'll grep for both markers."),
		call(ChatToolCall{Index: &idx, ID: "call_1", Function: ChatFunctionCall{Name: "exec"}}),
		call(ChatToolCall{Index: &idx, Function: ChatFunctionCall{Arguments: `{"input":"text(1)"}`}}),
	}
	for i := 0; i < len(mimoEchoedParamToolCall); i += 9 {
		end := i + 9
		if end > len(mimoEchoedParamToolCall) {
			end = len(mimoEchoedParamToolCall)
		}
		chunks = append(chunks, text(mimoEchoedParamToolCall[i:end]))
	}

	var events []ResponsesStreamEvent
	for _, ch := range chunks {
		events = append(events, ChatCompletionsChunkToResponsesEvents(ch, state)...)
	}
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)

	var streamed strings.Builder
	var call1 *ResponsesOutput
	for _, ev := range events {
		if ev.Type == "response.output_text.delta" {
			streamed.WriteString(ev.Delta)
		}
		if ev.Type == "response.output_item.done" && ev.Item != nil && ev.Item.Type == "custom_tool_call" {
			call1 = ev.Item
		}
	}
	assert.Equal(t, "I'll grep for both markers.", streamed.String())
	assert.NotContains(t, state.Text.String(), "<tool_call>")
	require.NotNil(t, call1)
	assert.Equal(t, "text(1)", call1.Input)
}

func TestChatCompletionsChunkToResponsesEvents_EchoOnlyContentOpensNoMessage(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("gpt-6-luna")
	state.CustomTools = map[string]bool{"exec": true}
	state.StripToolCallMarkup = true
	idx := 0
	echo := mimoEchoedToolCall
	events := ChatCompletionsChunkToResponsesEvents(&ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{
		ToolCalls: []ChatToolCall{{Index: &idx, ID: "call_1", Function: ChatFunctionCall{Name: "exec", Arguments: `{"input":"text(1)"}`}}},
	}}}}, state)
	events = append(events, ChatCompletionsChunkToResponsesEvents(&ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{Content: &echo}}}}, state)...)
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
	for _, ev := range events {
		if ev.Item != nil && ev.Item.Type == "message" {
			t.Fatalf("echo-only content must not open a message item: %s", ev.Type)
		}
	}
}

func TestChatCompletionsChunkToResponsesEvents_MarkupKeptWhenStripDisabled(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("m")
	echo := mimoEchoedToolCall
	ChatCompletionsChunkToResponsesEvents(&ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{Content: &echo}}}}, state)
	FinalizeChatCompletionsResponsesStream(state)
	assert.Equal(t, mimoEchoedToolCall, state.Text.String())
}

func TestStripResponsesToolCallMarkup(t *testing.T) {
	resp := &ResponsesResponse{Output: []ResponsesOutput{
		{Type: "message", Content: []ResponsesContentPart{{Type: "output_text", Text: mimoEchoedToolCall}}},
		{Type: "custom_tool_call", Name: "exec", Input: "text(1)"},
	}}
	StripResponsesToolCallMarkup(resp)
	require.Len(t, resp.Output, 1)
	assert.Equal(t, "custom_tool_call", resp.Output[0].Type)

	keep := &ResponsesResponse{Output: []ResponsesOutput{
		{Type: "message", Content: []ResponsesContentPart{{Type: "output_text", Text: "done." + mimoEchoedToolCall}}},
	}}
	StripResponsesToolCallMarkup(keep)
	require.Len(t, keep.Output, 1)
	assert.Equal(t, "done.", keep.Output[0].Content[0].Text)
}
