package chat_completions

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type aicoToOpenAIState struct {
	ID            string
	UnixTimestamp int64
	ContentSent   bool
}

// ConvertAICOResponseToOpenAI translates AICO streaming response chunk to OpenAI format.
func ConvertAICOResponseToOpenAI(_ context.Context, _ string, _, _, rawJSON []byte, param *any) []string {
	logrus.Debugf("aico translator: processing stream chunk: %s", string(rawJSON))
	if *param == nil {
		*param = &aicoToOpenAIState{
			ID:            fmt.Sprintf("aico-%d", time.Now().UnixNano()),
			UnixTimestamp: time.Now().Unix(),
		}
	}
	st := (*param).(*aicoToOpenAIState)

	// Base OpenAI template for streaming chunk
	baseTemplate := `{"id":"","object":"chat.completion.chunk","created":0,"model":"aico","choices":[{"index":0,"delta":{"content":""},"finish_reason":null}]}`
	baseTemplate, _ = sjson.Set(baseTemplate, "id", st.ID)
	baseTemplate, _ = sjson.Set(baseTemplate, "created", st.UnixTimestamp)

	event := gjson.GetBytes(rawJSON, "event").String()
	
	// We only process workflow_finished to avoid duplicates from node_finished.
	// workflow_finished is the most authoritative final state.
	if event == "workflow_finished" && !st.ContentSent {
		st.ContentSent = true
		
		// Extract text from stringified JSON in data.output
		outputJSONStr := gjson.GetBytes(rawJSON, "data.output").String()
		
		var chunks []string
		if outputJSONStr != "" {
			thinking := gjson.Get(outputJSONStr, "thinking").String()
			content := gjson.Get(outputJSONStr, "output").String()
			
			// Fallback if it's not a JSON string
			if content == "" && !gjson.Get(outputJSONStr, "output").Exists() {
				content = outputJSONStr
			}

			if thinking != "" {
				chunk, _ := sjson.Set(baseTemplate, "choices.0.delta.reasoning_content", thinking)
				chunk, _ = sjson.Delete(chunk, "choices.0.delta.content")
				chunks = append(chunks, chunk)
			}
			if content != "" {
				chunk, _ := sjson.Set(baseTemplate, "choices.0.delta.content", content)
				chunks = append(chunks, chunk)
			}
		}

		// Final stop chunk
		final, _ := sjson.Set(baseTemplate, "choices.0.delta", map[string]interface{}{})
		final, _ = sjson.Set(final, "choices.0.finish_reason", "stop")
		chunks = append(chunks, final)
		
		return chunks
	}

	return nil
}

// ConvertAICOResponseToOpenAINonStream translates AICO non-streaming response to OpenAI format.
func ConvertAICOResponseToOpenAINonStream(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) string {
	logrus.Debugf("aico translator: processing non-stream body: %s", string(rawJSON))
	
	// Extract output JSON string
	// Priority: data.data.output (nested) > data.output
	outputJSONStr := gjson.GetBytes(rawJSON, "data.data.output").String()
	if outputJSONStr == "" {
		outputJSONStr = gjson.GetBytes(rawJSON, "data.output").String()
	}

	thinking := gjson.Get(outputJSONStr, "thinking").String()
	content := gjson.Get(outputJSONStr, "output").String()
	if content == "" && !gjson.Get(outputJSONStr, "output").Exists() {
		content = outputJSONStr
	}

	// OpenAI Chat Completion response
	out := `{"id":"","object":"chat.completion","created":0,"model":"aico","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`
	out, _ = sjson.Set(out, "id", fmt.Sprintf("aico-%d", time.Now().UnixNano()))
	out, _ = sjson.Set(out, "created", time.Now().Unix())
	out, _ = sjson.Set(out, "choices.0.message.content", content)
	
	if thinking != "" {
		out, _ = sjson.Set(out, "choices.0.message.reasoning_content", thinking)
	}
	
	// Extract usage
	usagePath := "data.data.usage"
	if !gjson.GetBytes(rawJSON, usagePath).Exists() {
		usagePath = "data.usage"
	}
	if usage := gjson.GetBytes(rawJSON, usagePath); usage.Exists() {
		out, _ = sjson.SetRaw(out, "usage", usage.Raw)
	}

	return out
}
