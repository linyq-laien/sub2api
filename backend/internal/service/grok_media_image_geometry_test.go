package service

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"testing"
	"time"

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

// Codex image_gen deserializes Images responses into
// ImageResponse{created, background, quality, size, data} with every field
// required, while xAI only returns data (+ its own usage).
func TestNormalizeGrokImagesResponseForOpenAIClients(t *testing.T) {
	t.Parallel()

	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	raw := []byte(`{"data":[{"b64_json":"` + encoded + `","mime_type":"image/jpeg"}],"usage":{"cost_in_usd_ticks":400000000}}`)

	out := normalizeGrokImagesResponseForOpenAIClients(raw, time.Unix(1792072730, 0))

	require.Equal(t, int64(1792072730), gjson.GetBytes(out, "created").Int())
	require.Equal(t, "opaque", gjson.GetBytes(out, "background").String())
	require.Equal(t, "medium", gjson.GetBytes(out, "quality").String())
	require.Equal(t, "32x16", gjson.GetBytes(out, "size").String())
	require.Equal(t, encoded, gjson.GetBytes(out, "data.0.b64_json").String())
	require.Equal(t, int64(400000000), gjson.GetBytes(out, "usage.cost_in_usd_ticks").Int())
}

func TestNormalizeGrokImagesResponseForOpenAIClientsKeepsExistingFields(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"created":7,"background":"transparent","quality":"low","size":"1024x1024","data":[{"url":"https://images.test/a.png"}]}`)
	require.JSONEq(t, string(raw), string(normalizeGrokImagesResponseForOpenAIClients(raw, time.Unix(99, 0))))

	urlOnly := normalizeGrokImagesResponseForOpenAIClients([]byte(`{"data":[{"url":"https://images.test/a.png"}]}`), time.Unix(99, 0))
	require.Equal(t, int64(99), gjson.GetBytes(urlOnly, "created").Int())
	require.False(t, gjson.GetBytes(urlOnly, "size").Exists())

	for _, body := range []string{``, `not json`, `{"error":{"message":"x"}}`} {
		require.Equal(t, body, string(normalizeGrokImagesResponseForOpenAIClients([]byte(body), time.Unix(99, 0))))
	}
}
