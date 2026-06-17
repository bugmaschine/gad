package extractors

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

var difficulties = []int{6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 20}
var defaultTimeoutSec = 120
var defaultRuns = 2

// NOTE: go default test timer is 30s

func TestMeasureFilemoonPoWDifficulties(t *testing.T) {
	difficulties := envDifficulties("FILEMOON_POW_DIFFICULTIES", difficulties)
	runs := envInt("FILEMOON_POW_RUNS", 2)
	timeoutSec := float64(envInt("FILEMOON_POW_TIMEOUT_SEC", defaultTimeoutSec))

	t.Logf("measuring difficulties=%v runs=%d timeout=%.0fs", difficulties, runs, timeoutSec)

	for _, difficulty := range difficulties {
		var totalDuration time.Duration
		var totalAttempts int64
		successes := 0

		for run := 0; run < runs; run++ {
			nonce := fmt.Sprintf("test-nonce-d%d-run%d", difficulty, run)

			start := time.Now()
			solution := solveFilemoonPoW(nonce, difficulty, timeoutSec)
			duration := time.Since(start)

			if solution == "" {
				t.Fatalf("difficulty %d run %d timed out after %s", difficulty, run, duration)
			}

			solutionNumber, err := strconv.ParseInt(solution, 10, 64)
			if err != nil {
				t.Fatalf("difficulty %d run %d returned invalid solution %q: %v", difficulty, run, solution, err)
			}

			candidate := nonce + ":" + solution
			zeroBits := filemoonPoWLeadingZeroBits(candidate)
			if zeroBits < difficulty {
				t.Fatalf(
					"difficulty %d run %d returned solution %q with only %d leading zero bits",
					difficulty,
					run,
					solution,
					zeroBits,
				)
			}

			attempts := solutionNumber + 1
			totalDuration += duration
			totalAttempts += attempts
			successes++

			t.Logf(
				"difficulty=%d run=%d solution=%s attempts=%d zeroBits=%d duration=%s",
				difficulty,
				run,
				solution,
				attempts,
				zeroBits,
				duration,
			)
		}

		avgDuration := totalDuration / time.Duration(successes)
		avgAttempts := float64(totalAttempts) / float64(successes)
		hashesPerSecond := float64(totalAttempts) / totalDuration.Seconds()

		t.Logf(
			"RESULT difficulty=%d avgDuration=%s avgAttempts=%.2f hashesPerSecond=%.2f expectedAttempts=%d",
			difficulty,
			avgDuration,
			avgAttempts,
			hashesPerSecond,
			1<<difficulty,
		)
	}
}

func BenchmarkFilemoonPoW(b *testing.B) {
	difficulties := envDifficulties("FILEMOON_POW_DIFFICULTIES", difficulties)
	timeoutSec := float64(envInt("FILEMOON_POW_TIMEOUT_SEC", defaultTimeoutSec))

	for _, difficulty := range difficulties {
		b.Run(fmt.Sprintf("difficulty_%d", difficulty), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				nonce := fmt.Sprintf("bench-nonce-d%d-run%d", difficulty, i)

				solution := solveFilemoonPoW(nonce, difficulty, timeoutSec)
				if solution == "" {
					b.Fatalf("difficulty %d timed out", difficulty)
				}
			}
		})
	}
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}

	return value
}

func envDifficulties(name string, fallback []int) []int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}

	parts := strings.Split(raw, ",")
	difficulties := make([]int, 0, len(parts))

	for _, part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value < 0 {
			continue
		}

		difficulties = append(difficulties, value)
	}

	if len(difficulties) == 0 {
		return fallback
	}

	return difficulties
}
