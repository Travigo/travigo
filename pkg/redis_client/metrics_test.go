package redis_client

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
)

func TestRedisMetricsCountPipelineCommands(t *testing.T) {
	before := telemetry.Snapshot()
	hook := realtimeMetricsHook{}
	cmds := []redis.Cmder{redis.NewStringCmd(context.Background(), "get", "a"), redis.NewStringCmd(context.Background(), "get", "b")}
	cmds[0].SetErr(redis.Nil)
	err := hook.ProcessPipelineHook(func(context.Context, []redis.Cmder) error { return redis.Nil })(context.Background(), cmds)
	if err != redis.Nil {
		t.Fatal("hook altered result")
	}
	after := telemetry.Snapshot()
	if after.Counters["redis_commands"]-before.Counters["redis_commands"] != 2 || after.Counters["redis_errors"] != before.Counters["redis_errors"] {
		t.Fatal("wrong pipeline metrics")
	}
}
