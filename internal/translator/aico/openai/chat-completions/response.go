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
	DoneSent      bool
}

// ConvertAICOResponseToOpenAI translates AICO streaming response chunk to OpenAI format.
func ConvertAICOResponseToOpenAI(_ context.Context, modelName string, _, _, rawJSON []byte, param *any) []string {
	logrus.Debugf("aico translator: processing stream chunk: %s", string(rawJSON))
	if *param == nil {
		*param = &aicoToOpenAIState{
			ID:            fmt.Sprintf("aico-%d", time.Now().UnixNano()),
			UnixTimestamp: time.Now().Unix(),
		}
	}
	st := (*param).(*aicoToOpenAIState)

	// Base OpenAI template for streaming chunk
	baseTemplate := `{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{"content":""},"finish_reason":null}]}`
	baseTemplate, _ = sjson.Set(baseTemplate, "id", st.ID)
	baseTemplate, _ = sjson.Set(baseTemplate, "created", st.UnixTimestamp)
	baseTemplate, _ = sjson.Set(baseTemplate, "model", modelName)

	event := gjson.GetBytes(rawJSON, "event").String()
	if event == "" {
		return nil
	}

	var chunks []string

	// 1. Handle incremental content (New standard way in AICO)
	if event == "node_chunk" {
		deltaContent := gjson.GetBytes(rawJSON, "data.choices.0.delta.content").String()
		if deltaContent != "" {
			chunk, _ := sjson.Set(baseTemplate, "choices.0.delta.content", deltaContent)
			chunks = append(chunks, chunk)
		}
	}

	// 2. Handle End of Stream
	if event == "workflow_finished" && !st.DoneSent {
		st.DoneSent = true
		
		// Some workflows might put final text here, but usually it's in node_chunk.
		// We send a final empty chunk with stop reason.
		final, _ := sjson.Set(baseTemplate, "choices.0.delta", map[string]interface{}{})
		final, _ = sjson.Set(final, "choices.0.finish_reason", "stop")
		
		// Include usage if present in workflow_finished
		if usage := gjson.GetBytes(rawJSON, "data.usage"); usage.Exists() {
			final, _ = sjson.SetRaw(final, "usage", usage.Raw)
		} else if usage := gjson.GetBytes(rawJSON, "usage"); usage.Exists() {
			final, _ = sjson.SetRaw(final, "usage", usage.Raw)
		}
		
		chunks = append(chunks, final)
	}

	return chunks
}

// ConvertAICOResponseToOpenAINonStream translates AICO non-streaming response to OpenAI format.
func ConvertAICOResponseToOpenAINonStream(_ context.Context, modelName string, _, _, rawJSON []byte, _ *any) string {
	logrus.Debugf("aico translator: processing non-stream body: %s", string(rawJSON))
	
	// Extract output string
	output := gjson.GetBytes(rawJSON, "data.data.output").String()
	if output == "" {
		output = gjson.GetBytes(rawJSON, "data.output").String()
	}

	// Handle if it's still a JSON string (for backward compatibility)
	if gjson.Valid(output) {
		if content := gjson.Get(output, "output").String(); content != "" {
			output = content
		}
	}

	// OpenAI Chat Completion response
	out := `{"id":"","object":"chat.completion","created":0,"model":"","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`
	out, _ = sjson.Set(out, "id", fmt.Sprintf("aico-%d", time.Now().UnixNano()))
	out, _ = sjson.Set(out, "created", time.Now().Unix())
	out, _ = sjson.Set(out, "model", modelName)
	out, _ = sjson.Set(out, "choices.0.message.content", output)
	
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
