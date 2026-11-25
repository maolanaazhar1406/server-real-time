package config_test

import (
	"testing"

	"github.com/belajar/server-real-time/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigYAMLAndEnvOverride(t *testing.T) {
	t.Setenv("NODE_ID", "node-env-override")
	t.Setenv("SLOW_CONSUMER_POLICY", "disconnect")

	cfg, err := config.Load("../../configs/server.yaml")
	require.NoError(t, err)
	require.Equal(t, "node-env-override", cfg.Node.ID)
	require.Equal(t, "disconnect", cfg.Core.SlowConsumerPolicy)
	require.True(t, cfg.WebSocket.Enabled)
	require.True(t, cfg.TCP.Enabled)
	require.True(t, cfg.UDP.Enabled)
}
