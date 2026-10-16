package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAlphaSearchPlatformForGroup(t *testing.T) {
	for group, want := range map[string]string{
		service.PlatformOpenAI:    service.PlatformOpenAI,
		service.PlatformComposite: service.PlatformOpenAI,
		service.PlatformGrok:      service.PlatformGrok,
	} {
		got, ok := alphaSearchPlatformForGroup(group)
		require.True(t, ok, group)
		require.Equal(t, want, got, group)
	}
	for _, group := range []string{service.PlatformMiMo, service.PlatformAnthropic, service.PlatformGemini, ""} {
		_, ok := alphaSearchPlatformForGroup(group)
		require.False(t, ok, group)
	}
}
