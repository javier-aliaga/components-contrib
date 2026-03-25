/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/tmc/langchaingo/llms"

	"github.com/dapr/components-contrib/conversation"
	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/kit/logger"
	kmeta "github.com/dapr/kit/metadata"
)

// ClaudeCodeMetadata holds the configuration for the Claude Code component.
type ClaudeCodeMetadata struct {
	// Model to use (e.g. "claude-sonnet-4-20250514", "claude-opus-4-20250514").
	Model string `json:"model" mapstructure:"model"`

	// AllowedTools is a comma-separated list of tools to pre-approve
	// (e.g. "Read,Edit,Bash") to avoid interactive permission prompts.
	AllowedTools string `json:"allowedTools,omitempty" mapstructure:"allowedTools"`

	// MaxTurns limits the number of agentic turns. 0 means no limit.
	MaxTurns int `json:"maxTurns,omitempty" mapstructure:"maxTurns"`

	// CLIPath is the path to the claude CLI binary. Defaults to "claude".
	CLIPath string `json:"cliPath,omitempty" mapstructure:"cliPath"`
}

// ClaudeCode implements the conversation.Conversation interface by spawning
// the Claude Code CLI as a subprocess using --output-format stream-json.
type ClaudeCode struct {
	meta   ClaudeCodeMetadata
	logger logger.Logger
}

// NewClaudeCode creates a new Claude Code conversation component.
func NewClaudeCode(logger logger.Logger) conversation.Conversation {
	return &ClaudeCode{
		logger: logger,
	}
}

func (c *ClaudeCode) Init(_ context.Context, meta conversation.Metadata) error {
	m := ClaudeCodeMetadata{}
	if err := kmeta.DecodeMetadata(meta.Properties, &m); err != nil {
		return fmt.Errorf("claudecode: failed to decode metadata: %w", err)
	}

	m.Model = conversation.GetClaudeCodeModel(m.Model)

	if m.CLIPath == "" {
		m.CLIPath = "claude"
	}

	// Verify the CLI is available.
	if _, err := exec.LookPath(m.CLIPath); err != nil {
		return fmt.Errorf("claudecode: CLI not found at %q: %w", m.CLIPath, err)
	}

	c.meta = m
	return nil
}

func (c *ClaudeCode) Converse(ctx context.Context, req *conversation.Request) (*conversation.Response, error) {
	if req == nil || req.Message == nil || len(*req.Message) == 0 {
		return &conversation.Response{
			Outputs: []conversation.Result{},
		}, nil
	}

	prompt, systemPrompt := buildPrompt(req.Message)

	args := c.buildArgs(prompt, systemPrompt, req)

	c.logger.Debugf("claudecode: running: %s %v", c.meta.CLIPath, args)

	cmd := exec.CommandContext(ctx, c.meta.CLIPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claudecode: failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claudecode: failed to start subprocess: %w", err)
	}

	// Parse the stream-json output.
	var (
		textBuilder  strings.Builder
		model        string
		sessionID    string
		stopReason   string
		toolAccums   = make(map[int]*toolAccumulator) // keyed by content block index
		inputTokens  uint64
		outputTokens uint64
		cachedTokens uint64
	)

	scanner := bufio.NewScanner(stdout)
	// Allow larger lines for tool call JSON.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var sl streamLine
		if err := json.Unmarshal(line, &sl); err != nil {
			c.logger.Warnf("claudecode: failed to parse stream line: %v", err)
			continue
		}

		if sl.Type == "system" {
			if sl.Subtype == "api_retry" {
				c.logger.Warnf("claudecode: API retry: %s", sl.Error)
			}
			continue
		}

		if sl.Type != "stream_event" || sl.Event == nil {
			continue
		}

		if sl.SessionID != "" {
			sessionID = sl.SessionID
		}

		var evt streamEvent
		if err := json.Unmarshal(sl.Event, &evt); err != nil {
			c.logger.Warnf("claudecode: failed to parse event: %v", err)
			continue
		}

		switch evt.Type {
		case "message_start":
			if evt.Message != nil {
				if evt.Message.Model != "" {
					model = evt.Message.Model
				}
				if evt.Message.Usage != nil {
					inputTokens += evt.Message.Usage.InputTokens
					cachedTokens += evt.Message.Usage.CacheReadInputTokens
				}
			}

		case "content_block_start":
			if evt.ContentBlock != nil && evt.ContentBlock.Type == "tool_use" {
				toolAccums[evt.Index] = &toolAccumulator{
					Index: evt.Index,
					ID:    evt.ContentBlock.ID,
					Name:  evt.ContentBlock.Name,
				}
			}

		case "content_block_delta":
			if evt.Delta == nil {
				continue
			}
			switch evt.Delta.Type {
			case "text_delta":
				textBuilder.WriteString(evt.Delta.Text)
			case "input_json_delta":
				if acc, ok := toolAccums[evt.Index]; ok {
					acc.JSONBuilder.WriteString(evt.Delta.PartialJSON)
				}
			}

		case "message_delta":
			if evt.Delta != nil && evt.Delta.StopReason != "" {
				stopReason = evt.Delta.StopReason
			}
			if evt.Usage != nil {
				outputTokens += evt.Usage.OutputTokens
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("claudecode: error reading stdout: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("claudecode: subprocess exited with error: %w", err)
	}

	// Build the response.
	finishReason := mapStopReason(stopReason)

	choice := conversation.Choice{
		FinishReason: finishReason,
		Index:        0,
		Message: conversation.Message{
			Content: textBuilder.String(),
		},
	}

	// Attach tool calls sorted by content block index for deterministic ordering.
	if len(toolAccums) > 0 {
		sorted := make([]*toolAccumulator, 0, len(toolAccums))
		for _, acc := range toolAccums {
			sorted = append(sorted, acc)
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Index < sorted[j].Index
		})

		toolCalls := make([]llms.ToolCall, 0, len(sorted))
		for _, acc := range sorted {
			toolCalls = append(toolCalls, llms.ToolCall{
				ID:   acc.ID,
				Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      acc.Name,
					Arguments: acc.JSONBuilder.String(),
				},
			})
		}
		choice.Message.ToolCallRequest = &toolCalls
	}

	usage := &conversation.Usage{
		PromptTokens:     inputTokens,
		CompletionTokens: outputTokens,
		TotalTokens:      inputTokens + outputTokens,
	}
	if cachedTokens > 0 {
		usage.PromptTokensDetails = &conversation.PromptTokensDetails{
			CachedTokens: cachedTokens,
		}
	}

	return &conversation.Response{
		Outputs: []conversation.Result{
			{
				StopReason: finishReason,
				Choices:    []conversation.Choice{choice},
			},
		},
		Model:               model,
		ConversationContext: sessionID,
		Usage:               usage,
	}, nil
}

func (c *ClaudeCode) GetComponentMetadata() (metadataInfo metadata.MetadataMap) {
	metadataStruct := ClaudeCodeMetadata{}
	metadata.GetMetadataInfoFromStructType(reflect.TypeOf(metadataStruct), &metadataInfo, metadata.ConversationType)
	return
}

// Close is a no-op. ClaudeCode is stateless between Converse calls;
// each call spawns a fresh subprocess.
func (c *ClaudeCode) Close() error {
	return nil
}

// buildArgs constructs the CLI arguments for the claude subprocess.
func (c *ClaudeCode) buildArgs(prompt, systemPrompt string, req *conversation.Request) []string {
	args := []string{
		"--bare",
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--model", c.meta.Model,
	}

	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	if c.meta.AllowedTools != "" {
		args = append(args, "--allowedTools", c.meta.AllowedTools)
	}

	if c.meta.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(c.meta.MaxTurns))
	}

	// Resume session if conversation context (session ID) is provided.
	if req.ConversationContext != "" {
		args = append(args, "--resume", req.ConversationContext)
	}

	return args
}

// buildPrompt extracts text content from the conversation messages.
// System-role messages are returned separately so they can be passed
// via --system-prompt. All other text is joined into the prompt.
func buildPrompt(messages *[]llms.MessageContent) (prompt, systemPrompt string) {
	if messages == nil {
		return "", ""
	}

	var promptParts []string
	var systemParts []string

	for _, msg := range *messages {
		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llms.TextContent:
				if msg.Role == llms.ChatMessageTypeSystem {
					systemParts = append(systemParts, p.Text)
				} else {
					promptParts = append(promptParts, p.Text)
				}
			case llms.ToolCallResponse:
				promptParts = append(promptParts, fmt.Sprintf("[Tool %s result]: %s", p.Name, p.Content))
			}
		}
	}

	return strings.Join(promptParts, "\n"), strings.Join(systemParts, "\n")
}

// mapStopReason converts Claude API stop reasons to the standard finish reasons
// used by the conversation package.
func mapStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	default:
		if reason == "" {
			return "stop"
		}
		return reason
	}
}
