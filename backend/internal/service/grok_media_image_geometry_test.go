package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestApplyGrokImagineImageGeometryMapsOpenAISize(t *testing.T) {
	t.Parallel()

	out, err := applyGrokImagineImageGeometry([]byte(`{"model":"grok-imagine-image-2.0","prompt":"hi","size":"1152x1536"}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(out, "size").Exists())
	require.Equal(t, "2k", gjson.GetBytes(out, "resolution").String())
	require.Equal(t, "3:4", gjson.GetBytes(out, "aspect_ratio").String())
}

func TestApplyGrokImagineImageGeometryKeepsClientGeometry(t *testing.T) {
	t.Parallel()

	out, err := applyGrokImagineImageGeometry([]byte(`{"size":"1024x1024","resolution":"2K","aspect_ratio":"16:9"}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(out, "size").Exists())
	require.Equal(t, "2k", gjson.GetBytes(out, "resolution").String())
	require.Equal(t, "16:9", gjson.GetBytes(out, "aspect_ratio").String())
}

func TestSanitizeGrokMediaForwardBodyConvertsImageSize(t *testing.T) {
	t.Parallel()

	out, contentType, err := sanitizeGrokMediaForwardBody(
		GrokMediaEndpointImagesGenerations,
		[]byte(`{"model":"grok-imagine-image","prompt":"hi","size":"1024x1024"}`),
		"application/json",
	)
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.False(t, gjson.GetBytes(out, "size").Exists())
	require.Equal(t, "1k", gjson.GetBytes(out, "resolution").String())
	require.Equal(t, "1:1", gjson.GetBytes(out, "aspect_ratio").String())
}

func TestParseGrokMediaRequestKeepsImageResolutionOutOfVideoNormalize(t *testing.T) {
	t.Parallel()

	info := ParseGrokMediaRequest("application/json", []byte(`{"model":"grok-imagine-image-2.0","resolution":"2K","aspect_ratio":"16:9"}`))
	require.Equal(t, "2k", info.ImageResolution)
	require.Equal(t, "16:9", info.AspectRatio)
	require.Equal(t, VideoBillingResolution480P, info.Resolution)
}

func TestGrokImagineAspectRatioFromSize(t *testing.T) {
	t.Parallel()
	require.Equal(t, "1:1", grokImagineAspectRatioFromSize("1024x1024"))
	require.Equal(t, "3:4", grokImagineAspectRatioFromSize("1152x1536"))
	require.Equal(t, "4:3", grokImagineAspectRatioFromSize("1536x1152"))
	require.Equal(t, "16:9", grokImagineAspectRatioFromSize("1792x1024"))
}

// xAI image endpoints return data[].url unless asked otherwise, while Codex's
// image_gen extension strictly decodes data[].b64_json (OpenAI gpt-image default)
// and fails with "missing field `b64_json`" on a url-only response.
func TestSanitizeGrokMediaForwardBodyDefaultsImageResponseFormatToB64JSON(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []GrokMediaEndpoint{GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits} {
		out, _, err := sanitizeGrokMediaForwardBody(endpoint, []byte(`{"model":"grok-imagine-image-2.0","prompt":"hi"}`), "application/json")
		require.NoError(t, err)
		require.Equal(t, "b64_json", gjson.GetBytes(out, "response_format").String(), endpoint)
	}
}

func TestSanitizeGrokMediaForwardBodyKeepsExplicitImageResponseFormat(t *testing.T) {
	t.Parallel()

	out, _, err := sanitizeGrokMediaForwardBody(
		GrokMediaEndpointImagesGenerations,
		[]byte(`{"model":"grok-imagine-image-2.0","prompt":"hi","response_format":"url"}`),
		"application/json",
	)
	require.NoError(t, err)
	require.Equal(t, "url", gjson.GetBytes(out, "response_format").String())
}

func TestSanitizeGrokMediaForwardBodyLeavesVideoResponseFormatUnset(t *testing.T) {
	t.Parallel()

	out, _, err := sanitizeGrokMediaForwardBody(
		GrokMediaEndpointVideosGenerations,
		[]byte(`{"model":"grok-imagine-video","prompt":"waves"}`),
		"application/json",
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(out, "response_format").Exists())
}
