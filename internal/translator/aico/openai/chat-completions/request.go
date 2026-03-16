package chat_completions

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ConvertOpenAIRequestToAICO converts an OpenAI Chat Completions request (raw JSON)
// into an AICO workflow request JSON.
// idParam is expected to be the workflow ID.
func ConvertOpenAIRequestToAICO(workflowID string, inputRawJSON []byte, stream bool) []byte {
	rawJSON := inputRawJSON
	
	// Base AICO request structure
	out := []byte(`{"content":[],"stream":false}`)
	
	if stream {
		out, _ = sjson.SetBytes(out, "stream", true)
	}

	// Extract messages and build prompt
	messages := gjson.GetBytes(rawJSON, "messages")
	var promptBuilder strings.Builder
	
	messages.ForEach(func(key, value gjson.Result) bool {
		role := value.Get("role").String()
		content := value.Get("content").String()
		
		if role != "" && content != "" {
			if promptBuilder.Len() > 0 {
				promptBuilder.WriteString("\n\n")
			}
			roleTitle := role
			if len(role) > 0 {
				roleTitle = strings.ToUpper(role[:1]) + role[1:]
			}
			promptBuilder.WriteString(fmt.Sprintf("%s: %s", roleTitle, content))
		}
		return true
	})
	
	prompt := promptBuilder.String()
	
	// Default field names. 
	// Professional Tip: The Executor should handle custom field overrides 
	// by modifying the rawJSON before calling Translate, but since AICO's 
	// structure is an array of fields, we will keep it simple here.
	modelName := gjson.GetBytes(rawJSON, "model").String()
	
	contentFields := []map[string]interface{}{
		{
			"field_name": "model",
			"type":       "input",
			"value":      modelName,
		},
		{
			"field_name": "content",
			"type":       "input",
			"value":      prompt,
		},
	}
	
	jsonContent, _ := json.Marshal(contentFields)
	out, _ = sjson.SetRawBytes(out, "content", jsonContent)

	return out
}
