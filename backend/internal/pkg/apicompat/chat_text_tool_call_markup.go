package apicompat

import "strings"

// 部分 chat 上游（如 MiMo）在下发结构化 tool_calls 的同时，会把同一次调用以
// 模型原生标记 <tool_call><function=exec>…</function></tool_call> 回显进
// content。codex 把 content 原样当作助手正文展示并写入历史，后续轮次模型还会
// 模仿这段标记，因此桥在启用时从正文中剔除完整的标记块。
const (
	chatToolCallMarkupOpen  = "<tool_call>"
	chatToolCallMarkupClose = "</tool_call>"
)

// chatToolCallMarkupFilter 是流式正文过滤器：完整的 <tool_call>…</tool_call>
// 块被丢弃，其余文本原样放行。增量可能在标记中途切开，可能构成开标记前缀的
// 尾部会被暂扣到下一个增量；未闭合的块在流收尾时原样补回，避免误吞正文。
type chatToolCallMarkupFilter struct {
	pending strings.Builder
	inBlock bool
}

// Push 接收一段正文增量，返回可立即下发的文本。
func (f *chatToolCallMarkupFilter) Push(delta string) string {
	_, _ = f.pending.WriteString(delta)
	buf := f.pending.String()
	var out strings.Builder
	for {
		if f.inBlock {
			end := strings.Index(buf, chatToolCallMarkupClose)
			if end < 0 {
				break
			}
			buf = buf[end+len(chatToolCallMarkupClose):]
			f.inBlock = false
			continue
		}
		start := strings.Index(buf, chatToolCallMarkupOpen)
		if start < 0 {
			keep := partialMarkupSuffixLen(buf, chatToolCallMarkupOpen)
			_, _ = out.WriteString(buf[:len(buf)-keep])
			buf = buf[len(buf)-keep:]
			break
		}
		_, _ = out.WriteString(buf[:start])
		buf = buf[start:]
		f.inBlock = true
	}
	f.pending.Reset()
	_, _ = f.pending.WriteString(buf)
	return out.String()
}

// Flush 在流收尾时返回所有暂扣文本（含未闭合的块），过滤器随之复位。
func (f *chatToolCallMarkupFilter) Flush() string {
	rest := f.pending.String()
	f.pending.Reset()
	f.inBlock = false
	return rest
}

// StripChatToolCallMarkup 剔除整段正文中完整的 <tool_call>…</tool_call> 块，
// 供非流式响应使用；未闭合的块原样保留。
func StripChatToolCallMarkup(text string) string {
	var f chatToolCallMarkupFilter
	return f.Push(text) + f.Flush()
}

// partialMarkupSuffixLen 返回 s 末尾与 marker 前缀重合的最大长度（不含完整匹配）。
func partialMarkupSuffixLen(s, marker string) int {
	max := len(marker) - 1
	if max > len(s) {
		max = len(s)
	}
	for n := max; n > 0; n-- {
		if strings.HasSuffix(s, marker[:n]) {
			return n
		}
	}
	return 0
}

// StripResponsesToolCallMarkup 对非流式转换结果做同样的剔除：message 文本去掉
// 完整的标记块；去除后为空且响应中已有工具调用时，丢弃这个空 message 项。
func StripResponsesToolCallMarkup(resp *ResponsesResponse) {
	if resp == nil {
		return
	}
	hasToolCall := false
	for _, item := range resp.Output {
		if item.Type != "message" && item.Type != "reasoning" {
			hasToolCall = true
			break
		}
	}
	out := resp.Output[:0]
	for _, item := range resp.Output {
		if item.Type == "message" {
			empty := true
			for i := range item.Content {
				item.Content[i].Text = StripChatToolCallMarkup(item.Content[i].Text)
				if item.Content[i].Text != "" {
					empty = false
				}
			}
			if empty && hasToolCall {
				continue
			}
		}
		out = append(out, item)
	}
	resp.Output = out
}
