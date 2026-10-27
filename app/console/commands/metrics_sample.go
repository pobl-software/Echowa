package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/shirou/gopsutil/v4/process"

	"goravel/app/metrics"
	"goravel/app/services"
)

// MetricsSample takes readings the way the server does, every 15 seconds,
// and prints the last as JSON on stdout. How long each took, and the CPU
// time they cost this process, go to stderr, so the JSON pipes into jq. It's for checking the figures by eye and
// measuring the collector's overhead (docs/verification.md, "Metrics"):
//
//	deploy/dev-vm.sh artisan metrics:sample
//	sudo echowa-artisan metrics:sample --readings 21
//
// The first reading has no rates, so it takes at least two.
type MetricsSample struct{}

func (r *MetricsSample) Signature() string {
	return "metrics:sample"
}

func (r *MetricsSample) Description() string {
	return "Read the server's and the apps' metrics, and print the last reading"
}

func (r *MetricsSample) Extend() command.Extend {
	return command.Extend{
		Category: "metrics",
		Flags: []command.Flag{
			&command.IntFlag{Name: "readings", Value: 2, Usage: "How many readings to take, 15 seconds apart (at least 2)"},
		},
	}
}

func (r *MetricsSample) Handle(ctx console.Context) error {
	readings := max(ctx.OptionInt("readings"), 2)
	collector := metrics.NewCollector(metrics.System{}, services.Docker(), metrics.Interval)
	self, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return err
	}
	cpuBefore, err := cpuTime(self)
	if err != nil {
		return err
	}

	var last metrics.Reading
	for i := range readings {
		if i > 0 {
			time.Sleep(metrics.Interval)
		}
		readCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		last, err = collector.Read(readCtx)
		cancel()
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
		fmt.Fprintf(os.Stderr, "reading %d of %d: host %s, docker %s (%d apps, %d other containers)\n",
			i+1, readings, last.Took.Host.Round(time.Microsecond), last.Took.Docker.Round(time.Microsecond), len(last.Apps), len(last.Others))
	}

	cpuAfter, err := cpuTime(self)
	if err != nil {
		return err
	}
	used := cpuAfter - cpuBefore
	fmt.Fprintf(os.Stderr, "CPU time: %s for %d readings, %s a reading\n", used.Round(time.Millisecond), readings, (used / time.Duration(readings)).Round(time.Microsecond))

	out, err := json.MarshalIndent(last, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))

	return nil
}

// cpuTime is the user and system CPU time the process has used.
func cpuTime(p *process.Process) (time.Duration, error) {
	times, err := p.Times()
	if err != nil {
		return 0, err
	}

	return time.Duration((times.User + times.System) * float64(time.Second)), nil
}
