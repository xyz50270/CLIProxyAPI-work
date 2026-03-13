package executor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	log "github.com/sirupsen/logrus"
)

// AICOExecutor implements a stateless executor for AICO AI provider.
type AICOExecutor struct {
	cfg *config.Config
}

// NewAICOExecutor creates a new AICO executor instance.
func NewAICOExecutor(cfg *config.Config) *AICOExecutor {
	return &AICOExecutor{cfg: cfg}
}

// Identifier returns the executor identifier.
func (e *AICOExecutor) Identifier() string { return "aico" }

// PrepareRequest injects AICO credentials into the outgoing HTTP request.
func (e *AICOExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	_, apiKey := e.resolveCredentials(auth)
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(req, attrs)
	return nil
}

// HttpRequest injects AICO credentials into the request and executes it.
func (e *AICOExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("aico executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if err := e.PrepareRequest(httpReq, auth); err != nil {
		return nil, err
	}
	httpClient := e.newHTTPClient(ctx, auth)
	return httpClient.Do(httpReq)
}

func (e *AICOExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	// For AICO, baseModel might be an alias. We need the actual workflow ID.
	requestedModel := thinking.ParseSuffix(req.Model).ModelName
	workflowID := e.resolveWorkflowID(auth, requestedModel)

	reporter := newUsageReporter(ctx, e.Identifier(), workflowID, auth)
	defer reporter.trackFailure(ctx, &err)

	baseURL, apiKey := e.resolveCredentials(auth)
	if baseURL == "" {
		baseURL = e.cfg.AICOEndpoint
	}

	from := opts.SourceFormat
	to := sdktranslator.FromString("aico")

	// Pass field name overrides via workflowID encoding
	contentField, modelField := e.resolveFieldNames(auth, requestedModel)
	targetModel := e.resolveTargetModel(auth, requestedModel)
	encodedID := fmt.Sprintf("%s|%s|%s|%s", workflowID, contentField, modelField, targetModel)

	translated := sdktranslator.TranslateRequest(from, to, encodedID, req.Payload, false)

	url := strings.TrimSuffix(baseURL, "/") + "/" + workflowID
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
	if err != nil {
		return resp, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	
	log.Debugf("aico executor: sending request to %s", url)
	log.Debugf("aico executor: request body: %s", string(translated))

	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      translated,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := e.newHTTPClient(ctx, auth)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("aico executor: close response body error: %v", errClose)
		}
	}()
	
	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		appendAPIResponseChunk(ctx, e.cfg, b)
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return resp, err
	}
	
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	appendAPIResponseChunk(ctx, e.cfg, body)
	log.Debugf("aico executor: non-stream raw response: %s", string(body))
	
	// Translate response back
	var param any
	out := sdktranslator.TranslateNonStream(ctx, to, from, workflowID, opts.OriginalRequest, translated, body, &param)
	resp = cliproxyexecutor.Response{Payload: []byte(out), Headers: httpResp.Header.Clone()}
	return resp, nil
}

func (e *AICOExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	// For AICO, baseModel might be an alias. We need the actual workflow ID.
	requestedModel := thinking.ParseSuffix(req.Model).ModelName
	workflowID := e.resolveWorkflowID(auth, requestedModel)

	reporter := newUsageReporter(ctx, e.Identifier(), workflowID, auth)
	defer reporter.trackFailure(ctx, &err)

	baseURL, apiKey := e.resolveCredentials(auth)
	if baseURL == "" {
		baseURL = e.cfg.AICOEndpoint
	}

	from := opts.SourceFormat
	to := sdktranslator.FromString("aico")

	// Pass field name overrides via workflowID encoding
	contentField, modelField := e.resolveFieldNames(auth, requestedModel)
	targetModel := e.resolveTargetModel(auth, requestedModel)
	encodedID := fmt.Sprintf("%s|%s|%s|%s", workflowID, contentField, modelField, targetModel)

	translated := sdktranslator.TranslateRequest(from, to, encodedID, req.Payload, true)

	url := strings.TrimSuffix(baseURL, "/") + "/" + workflowID
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	
	log.Debugf("aico executor: sending request to %s", url)
	log.Debugf("aico executor: request body: %s", string(translated))

	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      translated,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := e.newHTTPClient(ctx, auth)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	
	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		appendAPIResponseChunk(ctx, e.cfg, b)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("aico executor: close response body error: %v", errClose)
		}
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return nil, err
	}
	
	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("aico executor: close response body error: %v", errClose)
			}
		}()
		
		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(nil, 52_428_800) // 50MB
		var param any
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			appendAPIResponseChunk(ctx, e.cfg, line)
			log.Debugf("aico executor: stream raw chunk: %s", string(line))
			
			// AICO SSE might have "data: " prefix or just be JSON.
			// The spec doesn't explicitly say "data: " but it says "streaming (SSE)".
			// Usually SSE means "data: " prefix.
			dataLine := line
			if bytes.HasPrefix(line, []byte("data:")) {
				dataLine = bytes.TrimSpace(line[5:])
			}
			
			if len(dataLine) == 0 {
				continue
			}

			chunks := sdktranslator.TranslateStream(ctx, to, from, workflowID, opts.OriginalRequest, translated, bytes.Clone(dataLine), &param)
			for i := range chunks {
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunks[i])}
			}
		}
		if errScan := scanner.Err(); errScan != nil {
			recordAPIResponseError(ctx, e.cfg, errScan)
			out <- cliproxyexecutor.StreamChunk{Err: errScan}
		}
	}()
	
	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}, nil
}

// CountTokens implements token counting for AICO.
// Currently it returns an error as AICO may not have a dedicated token count endpoint.
func (e *AICOExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, fmt.Errorf("aico executor: CountTokens not implemented")
}

// Refresh implements credential refreshing for AICO.
// Since AICO uses static API keys, this is a no-op that returns the original auth.
func (e *AICOExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	return auth, nil
}

func (e *AICOExecutor) resolveWorkflowID(auth *cliproxyauth.Auth, modelName string) string {
	if auth == nil || e.cfg == nil {
		return modelName
	}
	_, apiKey := e.resolveCredentials(auth)
	if apiKey == "" {
		return modelName
	}

	for i := range e.cfg.AICOKey {
		entry := &e.cfg.AICOKey[i]
		if strings.EqualFold(strings.TrimSpace(entry.APIKey), apiKey) {
			for j := range entry.Models {
				m := &entry.Models[j]
				if strings.EqualFold(strings.TrimSpace(m.Alias), modelName) {
					if name := strings.TrimSpace(m.Name); name != "" {
						return name
					}
				}
			}
		}
	}
	return modelName
}

func (e *AICOExecutor) resolveFieldNames(auth *cliproxyauth.Auth, modelName string) (contentField, modelField string) {
	contentField = "content"
	modelField = "model"

	if auth == nil || e.cfg == nil {
		return
	}
	_, apiKey := e.resolveCredentials(auth)
	if apiKey == "" {
		return
	}

	for i := range e.cfg.AICOKey {
		entry := &e.cfg.AICOKey[i]
		if strings.EqualFold(strings.TrimSpace(entry.APIKey), apiKey) {
			for j := range entry.Models {
				m := &entry.Models[j]
				if strings.EqualFold(strings.TrimSpace(m.Alias), modelName) {
					if m.ContentField != "" {
						contentField = m.ContentField
					}
					if m.ModelField != "" {
						modelField = m.ModelField
					}
					return
				}
			}
		}
	}
	return
}

func (e *AICOExecutor) resolveTargetModel(auth *cliproxyauth.Auth, modelName string) string {
	if auth == nil || e.cfg == nil {
		return modelName
	}
	_, apiKey := e.resolveCredentials(auth)
	if apiKey == "" {
		return modelName
	}

	for i := range e.cfg.AICOKey {
		entry := &e.cfg.AICOKey[i]
		if strings.EqualFold(strings.TrimSpace(entry.APIKey), apiKey) {
			for j := range entry.Models {
				m := &entry.Models[j]
				if strings.EqualFold(strings.TrimSpace(m.Alias), modelName) {
					if m.TargetModel != "" {
						return m.TargetModel
					}
					return modelName
				}
			}
		}
	}
	return modelName
}

func (e *AICOExecutor) resolveCredentials(auth *cliproxyauth.Auth) (baseURL, apiKey string) {
	if auth == nil {
		return "", ""
	}
	if auth.Attributes != nil {
		baseURL = strings.TrimSpace(auth.Attributes["base_url"])
		apiKey = strings.TrimSpace(auth.Attributes["api_key"])
	}
	return
}

func (e *AICOExecutor) newHTTPClient(ctx context.Context, auth *cliproxyauth.Auth) *http.Client {
	skipVerify := false
	if auth != nil && auth.Attributes != nil {
		if val, ok := auth.Attributes["insecure_skip_verify"]; ok && val == "true" {
			skipVerify = true
		}
	}

	if skipVerify {
		log.Debugf("aico executor: insecure-skip-verify is ENABLED for auth %s", auth.ID)
	}

	client := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	
	// If we need to skip verify, we MUST ensure the transport supports it.
	if skipVerify {
		if client.Transport == nil {
			client.Transport = http.DefaultTransport
		}
		
		// If it's a standard Transport, we can override TLSClientConfig.
		if tr, ok := client.Transport.(*http.Transport); ok {
			// Clone the transport to avoid polluting the global one if it's http.DefaultTransport
			newTr := tr.Clone()
			if newTr.TLSClientConfig == nil {
				newTr.TLSClientConfig = &tls.Config{}
			}
			newTr.TLSClientConfig.InsecureSkipVerify = true
			client.Transport = newTr
		} else {
			log.Warnf("aico executor: could not apply insecure-skip-verify: transport type %T is not *http.Transport", client.Transport)
		}
	}
	return client
}
