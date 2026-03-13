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
func ConvertOpenAIRequestToAICO(idParam string, inputRawJSON []byte, stream bool) []byte {
	// Note: workflowID from idParam is currently not used in the body, 
	// but might be used if AICO requires it in the content array.
	// For now we keep the decoding logic but avoid the 'unused' error.
	
	contentFieldName := "content"
	modelFieldName := "model"
	targetModelName := ""

	// Decode encoded field names if present: "id|contentField|modelField|targetModel"
	if strings.Contains(idParam, "|") {
		parts := strings.Split(idParam, "|")
		if len(parts) >= 2 && parts[1] != "" {
			contentFieldName = parts[1]
		}
		if len(parts) >= 3 && parts[2] != "" {
			modelFieldName = parts[2]
		}
		if len(parts) >= 4 && parts[3] != "" {
			targetModelName = parts[3]
		}
	}

	rawJSON := inputRawJSON
	
	// Base AICO request structure
	out := []byte(`{"content":[],"stream":false}`)
	
	if stream {
		out, _ = sjson.SetBytes(out, "stream", true)
	}

	// Extract messages from OpenAI request
	messages := gjson.GetBytes(rawJSON, "messages")
	var promptBuilder strings.Builder
	
	messages.ForEach(func(key, value gjson.Result) bool {
		role := value.Get("role").String()
		content := value.Get("content").String()
		
		if role != "" && content != "" {
			if promptBuilder.Len() > 0 {
				promptBuilder.WriteString("\n\n")
			}
			// Simple role title case
			roleTitle := role
			if len(role) > 0 {
				roleTitle = strings.ToUpper(role[:1]) + role[1:]
			}
			// Format as "Role: Content"
			promptBuilder.WriteString(fmt.Sprintf("%s: %s", roleTitle, content))
		}
		return true
	})
	
	prompt := promptBuilder.String()
	
	// AICO content fields
	// 1. model (actual model name mapped from alias)
	if targetModelName == "" {
		targetModelName = gjson.GetBytes(rawJSON, "model").String()
	}
	
	contentFields := []map[string]interface{}{
		{
			"field_name": modelFieldName,
			"type":       "input",
			"value":      targetModelName,
		},
		{
			"field_name": contentFieldName,
			"type":       "input",
			"value":      prompt,
		},
	}
	
	jsonContent, _ := json.Marshal(contentFields)
	out, _ = sjson.SetRawBytes(out, "content", jsonContent)

	return out
}
