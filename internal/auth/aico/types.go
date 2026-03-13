package aico

// Request represents the AICO API request body.
type Request struct {
	Content []ContentField `json:"content"`
	Stream  bool           `json:"stream"`
}

// ContentField represents an input field in the AICO API request.
type ContentField struct {
	FieldName string `json:"field_name"`
	Type      string `json:"type"`
	Required  bool   `json:"required,omitempty"`
	Value     string `json:"value"`
}

// Response represents the AICO API non-streaming response body.
type Response struct {
	Code int          `json:"code"`
	Msg  string       `json:"msg"`
	Data ResponseData `json:"data"`
}

// StreamResponse represents the AICO API streaming response chunk.
type StreamResponse struct {
	Event           string       `json:"event"`
	SessionID       string       `json:"session_id"`
	StepID          string       `json:"step_id"`
	Created         int64        `json:"created"`
	Data            ResponseData `json:"data"`
	Usage           Usage        `json:"usage"`
	ModelStopReason string       `json:"model_stop_reason,omitempty"`
}

// ResponseData represents the data field in AICO API response.
type ResponseData struct {
	Text   []string `json:"text"`
	Output bool     `json:"output"`
}

// Usage represents the token usage information in AICO API response.
type Usage struct {
	TotalTokens      int    `json:"total_tokens"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	MaxToken         string `json:"max_token,omitempty"`
}
