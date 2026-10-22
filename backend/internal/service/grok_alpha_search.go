package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
)

// forwardGrokAlphaSearch serves Codex standalone web.run (/alpha/search) from a
// Grok account. xAI has no standalone search endpoint, so the request runs as a
// Responses turn with the hosted web_search tool; xAI streams the same
// output_text and url_citation events as OpenAI, so the result is converted
// with the shared alpha search parser.
func (s *OpenAIGatewayService) forwardGrokAlphaSearch(ctx context.Context, c *gin.Context, account *Account, alphaBody []byte, requestedModel string) (*OpenAIForwardResult, error) {
	upstreamModel := account.GetMappedModel(requestedModel)
	if strings.TrimSpace(upstreamModel) == "" {
		upstreamModel = grokDefaultResponsesModel
	}
	upstreamModel = xai.ResolveGrokTextResponsesModelID(upstreamModel, grokDefaultResponsesModel)
	if isGrokImageGenerationModel(upstreamModel) {
		return nil, fmt.Errorf("model %s is an image model and cannot run web search", upstreamModel)
	}
	responsesBody, err := buildOpenAIAlphaSearchResponsesWebSearchBody(alphaBody, upstreamModel)
	if err != nil {
		return nil, err
	}
	SetOpsUpstreamModel(c, upstreamModel)
	SetActualOpenAIUpstreamEndpoint(c, "/v1/responses")

	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	req, err := buildGrokResponsesRequest(upstreamCtx, c, account, responsesBody, token, "", s.cfg, s.settingService)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")

	upstreamStart := time.Now()
	resp, err := s.doOpenAIUpstream(req, proxyURL, account)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, fmt.Errorf("read grok alpha search response: %w", err)
	}

	stateCtx := withGrokTeamRateLimitModel(ctx, upstreamModel)
	if resp.StatusCode >= http.StatusBadRequest {
		upstreamMsg := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(respBody))
		if upstreamMsg == "" {
			upstreamMsg = fmt.Sprintf("upstream returned status %d", resp.StatusCode)
		}
		failover := s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)
		kind := "http_error"
		if failover {
			kind = "failover"
		}
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			ProxyID:            opsUpstreamProxyID(account),
			ProxyName:          opsUpstreamProxyName(account),
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
			Kind:               kind,
			Message:            upstreamMsg,
		})
		s.handleGrokAccountUpstreamError(stateCtx, account, resp.StatusCode, resp.Header, respBody)
		if failover {
			retryable, retryDelay, retryDeadline, retryMax := grokSameAccountRetryMetadata(account, resp.StatusCode, respBody)
			return nil, &UpstreamFailoverError{
				StatusCode:               resp.StatusCode,
				ResponseBody:             respBody,
				ResponseHeaders:          resp.Header.Clone(),
				RetryableOnSameAccount:   retryable,
				SameAccountRetryDelay:    retryDelay,
				SameAccountRetryDeadline: retryDeadline,
				SameAccountRetryMax:      retryMax,
			}
		}
		setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, "")
		writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		c.Data(resp.StatusCode, contentType, respBody)
		return nil, nil
	}

	s.updateGrokUsageFromResponse(stateCtx, account, resp.Header, resp.StatusCode)
	alphaRespBody, err := openAIAlphaSearchResponseFromResponsesSSE(respBody)
	if err != nil {
		return nil, err
	}
	c.Data(http.StatusOK, "application/json", alphaRespBody)
	return &OpenAIForwardResult{
		RequestID:        firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
		UpstreamHeaders:  resp.Header,
		Model:            requestedModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: "/v1/responses",
		ResponseHeaders:  resp.Header.Clone(),
		Duration:         time.Since(upstreamStart),
		WebSearchCalls:   1,
	}, nil
}
