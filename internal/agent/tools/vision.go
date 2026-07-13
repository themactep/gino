package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wltechblog/gino/internal/providers"
)

// VisionTool lets the agent analyse images by calling a vision-capable model.
// The tool reads an image file, encodes it as a base64 data URL, and sends it
// to the configured vision model in a separate LLM call. The model's text
// description is returned as the tool result.
type VisionTool struct {
	provider    providers.LLMProvider
	visionModel string
}

// NewVisionTool creates a vision analysis tool. If visionModel is empty the
// tool is not registered (caller's responsibility).
func NewVisionTool(provider providers.LLMProvider, visionModel string) *VisionTool {
	return &VisionTool{provider: provider, visionModel: visionModel}
}

func (t *VisionTool) Name() string { return "vision" }

func (t *VisionTool) Description() string {
	return `Analyse an image file using a vision-capable AI model.

Provide the path to an image file and optionally a question or instruction about what to look for. The tool reads the image, sends it to a vision model, and returns a text description.

Supported formats: .jpg, .jpeg, .png, .gif, .webp, .bmp. Maximum file size: 20MB.

Examples:
  - {"path": "/tmp/screenshot.png"} — describe what's in the image
  - {"path": "/tmp/diagram.png", "prompt": "What does this flowchart show?"}
  - {"path": "/tmp/error.jpg", "prompt": "Read the error message in this image"}`
}

func (t *VisionTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Absolute path to the image file to analyse.",
			},
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "Optional question or instruction about the image. If omitted, the model will provide a general description.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *VisionTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", fmt.Errorf("vision: 'path' argument required")
	}

	// Resolve path
	path = filepath.Clean(path)

	// Check file exists
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("vision: cannot access file %s: %w", path, err)
	}

	// Size limit: 20MB
	const maxSize = 20 * 1024 * 1024
	if info.Size() > maxSize {
		return "", fmt.Errorf("vision: file %s is %d bytes, exceeds 20MB limit", path, info.Size())
	}

	// Validate extension
	ext := strings.ToLower(filepath.Ext(path))
	mime, ok := imageMimeByExt(ext)
	if !ok {
		return "", fmt.Errorf("vision: unsupported image format %s (supported: jpg, jpeg, png, gif, webp, bmp)", ext)
	}

	// Read and encode
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("vision: failed to read %s: %w", path, err)
	}
	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)

	// Build prompt
	prompt := "Describe this image in detail. Include any text, objects, people, colours, layout, and notable features."
	if p, ok := args["prompt"].(string); ok && p != "" {
		prompt = p
	}

	// Call vision model
	messages := []providers.Message{
		{
			Role:    "user",
			Content: prompt,
			Images:  []string{dataURL},
		},
	}

	resp, err := t.provider.Chat(ctx, messages, nil, t.visionModel)
	if err != nil {
		return "", fmt.Errorf("vision: model call failed: %w", err)
	}

	if resp.Content == "" {
		return "", fmt.Errorf("vision: model returned empty response")
	}

	return fmt.Sprintf("Image: %s\nModel: %s\n\n%s", path, t.visionModel, resp.Content), nil
}

// imageMimeByExt maps an image file extension to its MIME type.
func imageMimeByExt(ext string) (string, bool) {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".png":
		return "image/png", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".bmp":
		return "image/bmp", true
	}
	return "", false
}
