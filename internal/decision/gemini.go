package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"
)

// thinkingBudgetTokens keeps a decision short. The default budget can run for
// minutes on flash models and time out the caller.
const thinkingBudgetTokens = 128

// Gemini calls the Gemini API with a JSON response schema.
type Gemini struct {
	client *genai.Client
	model  string
	// Log reports operational messages. A nil Log discards them.
	Log func(format string, args ...any)
}

// NewGemini builds a client for the Gemini Developer API.
func NewGemini(ctx context.Context, apiKey, model string) (*Gemini, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("GEMINI_API_KEY is not set")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("gemini model is not configured")
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("create gemini client: %w", err)
	}
	return &Gemini{client: client, model: model}, nil
}

// newWithClient is used by tests to inject a fake transport.
func newWithClient(client *genai.Client, model string) *Gemini {
	return &Gemini{client: client, model: model}
}

// Decide asks Gemini for one decision and validates the reply.
func (g *Gemini) Decide(ctx context.Context, request Request) (Result, error) {
	if g == nil || g.client == nil {
		return Result{}, errors.New("gemini is not configured")
	}
	prompt, redactions := BuildPrompt(request)
	started := time.Now()
	response, err := g.generate(ctx, prompt, request.Stage, true)
	if isThinkingConfigRejected(err) {
		g.logf("gemini %s rejected the thinking budget; retrying without it", request.Stage)
		response, err = g.generate(ctx, prompt, request.Stage, false)
	}
	g.logf("gemini %s decision took %s", request.Stage, time.Since(started).Round(time.Millisecond))
	if err != nil {
		return Result{}, fmt.Errorf("gemini request failed: %w", err)
	}
	result, err := decodeReply(response.Text())
	if err != nil {
		return Result{}, err
	}
	result.Model = g.model
	result.InputChars = len(prompt)
	result.Redactions = redactions
	return validate(request.Stage, result, request.PromptMax)
}

// generate performs one request, optionally capping the thinking budget.
func (g *Gemini) generate(ctx context.Context, prompt, stage string, withThinking bool) (*genai.GenerateContentResponse, error) {
	config := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(systemInstruction, genai.RoleUser),
		ResponseMIMEType:  "application/json",
		ResponseSchema:    schemaFor(stage),
		Temperature:       genai.Ptr[float32](0.2),
		MaxOutputTokens:   1024,
	}
	if withThinking {
		config.ThinkingConfig = &genai.ThinkingConfig{ThinkingBudget: genai.Ptr[int32](thinkingBudgetTokens)}
	}
	return g.client.Models.GenerateContent(ctx, g.model, genai.Text(prompt), config)
}

// isThinkingConfigRejected reports a 400, which is how an unsupported
// thinkingBudget comes back.
func isThinkingConfigRejected(err error) bool {
	var apiErr genai.APIError
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusBadRequest
}

func (g *Gemini) logf(format string, args ...any) {
	if g != nil && g.Log != nil {
		g.Log(format, args...)
	}
}

func decodeReply(text string) (Result, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Result{}, errors.New("gemini returned no decision")
	}
	var reply struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(trimmed), &reply); err != nil {
		return Result{}, errors.New("gemini returned invalid JSON")
	}
	return Result{Action: strings.TrimSpace(reply.Action), Reason: reply.Reason, Prompt: reply.Prompt}, nil
}

func schemaFor(stage string) *genai.Schema {
	actions := []string{ActionResume, ActionSkipDone, ActionSkipStuck}
	if stage == "post" {
		actions = []string{ActionDone, ActionResumeAgain, ActionStop}
	}
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"action": {
				Type:        genai.TypeString,
				Enum:        actions,
				Description: "The decision for this session.",
			},
			"reason": {
				Type:        genai.TypeString,
				Description: "One short sentence explaining the decision.",
			},
			"prompt": {
				Type:        genai.TypeString,
				Description: "Concrete next step for the agent. Empty unless resuming.",
			},
		},
		Required:         []string{"action", "reason", "prompt"},
		PropertyOrdering: []string{"action", "reason", "prompt"},
	}
}
