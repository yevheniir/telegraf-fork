package health_test

import (
	"testing"
	"time"

	"github.com/yevheniir/telegraf-fork"
	"github.com/yevheniir/telegraf-fork/plugins/outputs/health"
	"github.com/yevheniir/telegraf-fork/testutil"
	"github.com/stretchr/testify/require"
)

func TestFieldFound(t *testing.T) {
	metrics := []telegraf.Metric{
		testutil.MustMetric(
			"cpu",
			map[string]string{},
			map[string]interface{}{
				"time_idle": 42.0,
			},
			time.Now()),
	}

	contains := &health.Contains{
		Field: "time_idle",
	}
	result := contains.Check(metrics)
	require.True(t, result)
}

func TestFieldNotFound(t *testing.T) {
	metrics := []telegraf.Metric{
		testutil.MustMetric(
			"cpu",
			map[string]string{},
			map[string]interface{}{},
			time.Now()),
	}

	contains := &health.Contains{
		Field: "time_idle",
	}
	result := contains.Check(metrics)
	require.False(t, result)
}

func TestOneMetricWithFieldIsSuccess(t *testing.T) {
	metrics := []telegraf.Metric{
		testutil.MustMetric(
			"cpu",
			map[string]string{},
			map[string]interface{}{},
			time.Now()),
		testutil.MustMetric(
			"cpu",
			map[string]string{},
			map[string]interface{}{
				"time_idle": 42.0,
			},
			time.Now()),
	}

	contains := &health.Contains{
		Field: "time_idle",
	}
	result := contains.Check(metrics)
	require.True(t, result)
}
