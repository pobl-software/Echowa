package feature

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/suite"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type ScheduledTasksTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
	app    *models.App
}

func TestScheduledTasksTestSuite(t *testing.T) {
	suite.Run(t, new(ScheduledTasksTestSuite))
}

func (s *ScheduledTasksTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	s.app = createTestApp(s.T(), "blog", nil)
	s.T().Cleanup(services.WaitForTaskRuns)
}

// execs scripts ExecTo with a command that writes output and exits with
// code, and records what it was asked to run.
type execs struct {
	sync.Mutex
	calls []string
}

func (s *ScheduledTasksTestSuite) succeed(output string, code int) *execs {
	e := &execs{}
	s.docker.ExecToFunc = func(_ context.Context, service string, cmd []string, out io.Writer) (int, error) {
		e.Lock()
		e.calls = append(e.calls, service+": "+strings.Join(cmd, " | "))
		e.Unlock()
		_, _ = io.WriteString(out, output)
		return code, nil
	}

	return e
}

func (s *ScheduledTasksTestSuite) addTask(app *models.App, schedule, zone string) *models.ScheduledTask {
	task := &models.ScheduledTask{
		AppID: app.ID, Name: "Scheduler", Schedule: schedule, Timezone: zone,
		Command: "php artisan schedule:run", TimeoutMinutes: 15, Enabled: true,
	}
	s.Require().NoError(facades.Orm().Query().Create(task))

	return task
}

func (s *ScheduledTasksTestSuite) runDue(now string) {
	at, err := time.Parse("2006-01-02 15:04", now)
	s.Require().NoError(err)
	s.Require().NoError(services.RunDueTasks(context.Background(), at))
	services.WaitForTaskRuns()
}

func (s *ScheduledTasksTestSuite) runs(task *models.ScheduledTask) []models.ScheduledTaskRun {
	var runs []models.ScheduledTaskRun
	s.Require().NoError(facades.Orm().Query().Where("scheduled_task_id", task.ID).Order("id").Get(&runs))

	return runs
}

func (s *ScheduledTasksTestSuite) TestADueTaskRunsInTheAppsContainer() {
	calls := s.succeed("Running scheduled command\n", 0)
	task := s.addTask(s.app, "0 * * * *", "UTC")

	s.runDue("2027-01-05 10:00")
	s.runDue("2027-01-05 10:01")

	s.Equal([]string{"echowa-blog: sh | -c | php artisan schedule:run"}, calls.calls, "once, at 10:00, with sh -c")
	runs := s.runs(task)
	s.Require().Len(runs, 1)
	run := runs[0]
	s.Equal(models.RunSucceeded, run.Status)
	s.Equal(models.RunOnSchedule, run.Trigger)
	s.Require().NotNil(run.ExitCode)
	s.Equal(0, *run.ExitCode)
	s.Equal("Running scheduled command\n", run.Output)
	s.Nil(run.Message)
	s.NotNil(run.StartedAt)
	s.NotNil(run.FinishedAt)
}

func (s *ScheduledTasksTestSuite) TestTheScheduleIsReadInTheTasksZone() {
	s.succeed("", 0)
	task := s.addTask(s.app, "0 9 * * *", "America/New_York")

	s.runDue("2027-01-05 09:00")
	s.Empty(s.runs(task), "09:00 UTC is 04:00 in New York")
	s.runDue("2027-01-05 14:00")
	s.Len(s.runs(task), 1, "14:00 UTC is 09:00 in New York")
}

func (s *ScheduledTasksTestSuite) TestDisabledTasksAndTemplateAppsNeverRun() {
	calls := s.succeed("", 0)
	disabled := s.addTask(s.app, "* * * * *", "UTC")
	_, err := facades.Orm().Query().Model(disabled).Update("enabled", false)
	s.Require().NoError(err)
	tmpl, _ := templates.Find("postgres")
	db, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "db", Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)
	s.addTask(db, "* * * * *", "UTC")

	s.runDue("2027-01-05 10:00")

	s.Empty(calls.calls)
	var n int64
	n, err = facades.Orm().Query().Model(&models.ScheduledTaskRun{}).Count()
	s.Require().NoError(err)
	s.Zero(n)
}

func (s *ScheduledTasksTestSuite) TestATaskNeverOverlapsItself() {
	release := make(chan struct{})
	started := make(chan struct{}, 10)
	s.docker.ExecToFunc = func(context.Context, string, []string, io.Writer) (int, error) {
		started <- struct{}{}
		<-release
		return 0, nil
	}
	task := s.addTask(s.app, "* * * * *", "UTC")
	ctx := context.Background()

	s.Require().NoError(services.RunDueTasks(ctx, time.Now()))
	<-started
	s.Require().NoError(services.RunDueTasks(ctx, time.Now().Add(time.Minute)))
	_, err := services.RunTask(s.app, task)
	s.ErrorIs(err, services.ErrTaskRunning)
	close(release)
	services.WaitForTaskRuns()

	runs := s.runs(task)
	s.Require().Len(runs, 1, "the second minute was skipped")
	s.Equal(models.RunSucceeded, runs[0].Status)

	run, err := services.RunTask(s.app, task)
	s.Require().NoError(err, "free again once the run ends")
	services.WaitForTaskRuns()
	s.Equal(models.RunByHand, run.Trigger)
	s.Len(s.runs(task), 2)
}

func (s *ScheduledTasksTestSuite) TestRunNowRunsADisabledTask() {
	s.succeed("ok\n", 0)
	task := s.addTask(s.app, "0 0 1 1 *", "UTC")
	task.Enabled = false

	_, err := services.RunTask(s.app, task)
	s.Require().NoError(err)
	services.WaitForTaskRuns()

	runs := s.runs(task)
	s.Require().Len(runs, 1)
	s.Equal(models.RunByHand, runs[0].Trigger)
	s.Equal(models.RunSucceeded, runs[0].Status)
}

func (s *ScheduledTasksTestSuite) TestAnAppWithNoRunningTaskSkipsTheRun() {
	task := s.addTask(s.app, "* * * * *", "UTC")

	s.runDue("2027-01-05 10:00")

	runs := s.runs(task)
	s.Require().Len(runs, 1)
	s.Equal(models.RunSkipped, runs[0].Status)
	s.Nil(runs[0].ExitCode)
	s.Require().NotNil(runs[0].Message)
	s.Equal("The app has no running task to run in.", *runs[0].Message)
}

func (s *ScheduledTasksTestSuite) TestANonZeroExitFails() {
	s.succeed("SQLSTATE[HY000] [2002] Connection refused\n", 3)
	task := s.addTask(s.app, "* * * * *", "UTC")

	s.runDue("2027-01-05 10:00")

	run := s.runs(task)[0]
	s.Equal(models.RunFailed, run.Status)
	s.Require().NotNil(run.ExitCode)
	s.Equal(3, *run.ExitCode)
	s.Contains(run.Output, "Connection refused")
}

func (s *ScheduledTasksTestSuite) TestARunStopsAtItsTimeout() {
	var deadline time.Time
	s.docker.ExecToFunc = func(ctx context.Context, _ string, _ []string, out io.Writer) (int, error) {
		deadline, _ = ctx.Deadline()
		_, _ = io.WriteString(out, "still going\n")
		return 0, context.DeadlineExceeded
	}
	task := s.addTask(s.app, "* * * * *", "UTC")
	_, err := facades.Orm().Query().Model(task).Update("timeout_minutes", 3)
	s.Require().NoError(err)

	start := time.Now()
	s.runDue("2027-01-05 10:00")

	s.WithinDuration(start.Add(3*time.Minute), deadline, 5*time.Second, "the task's own timeout")
	run := s.runs(task)[0]
	s.Equal(models.RunTimedOut, run.Status)
	s.Nil(run.ExitCode)
	s.Equal("still going\n", run.Output, "what it wrote before the timeout is kept")
	s.Require().NotNil(run.Message)
	s.Equal("Stopped waiting after 3 minutes. The command may still be running in the container.", *run.Message)
}

func (s *ScheduledTasksTestSuite) TestOnlyTheEndOfALongOutputIsKept() {
	s.docker.ExecToFunc = func(_ context.Context, _ string, _ []string, out io.Writer) (int, error) {
		for i := range 20_000 {
			_, _ = fmt.Fprintf(out, "line %05d\n", i)
		}
		return 0, nil
	}
	task := s.addTask(s.app, "* * * * *", "UTC")

	s.runDue("2027-01-05 10:00")

	output := s.runs(task)[0].Output
	s.LessOrEqual(len(output), services.MaxTaskOutput+100)
	s.True(strings.HasPrefix(output, "["), "it says what it dropped")
	s.True(strings.HasSuffix(output, "line 19999\n"))
}

func (s *ScheduledTasksTestSuite) TestRestartMarksAnEarlierProcesssRunsInterrupted() {
	task := s.addTask(s.app, "* * * * *", "UTC")
	create := func(started time.Time) *models.ScheduledTaskRun {
		run := &models.ScheduledTaskRun{
			ScheduledTaskID: task.ID, Trigger: models.RunOnSchedule, Status: models.RunRunning,
			StartedAt: carbon.NewDateTime(carbon.FromStdTime(started)),
		}
		s.Require().NoError(facades.Orm().Query().Create(run))
		return run
	}
	old := create(time.Now().Add(-time.Hour))
	current := create(time.Now().Add(time.Second))

	s.Require().NoError(services.RecoverTaskRuns(context.Background()))

	var got models.ScheduledTaskRun
	s.Require().NoError(facades.Orm().Query().Find(&got, old.ID))
	s.Equal(models.RunInterrupted, got.Status)
	s.NotNil(got.FinishedAt)
	s.Require().NotNil(got.Message)
	s.Contains(*got.Message, "Echowa restarted")
	var kept models.ScheduledTaskRun
	s.Require().NoError(facades.Orm().Query().Find(&kept, current.ID))
	s.Equal(models.RunRunning, kept.Status, "this process's runs are left alone")
}

func (s *ScheduledTasksTestSuite) TestPruningKeepsEachTasksNewestRuns() {
	busy := s.addTask(s.app, "* * * * *", "UTC")
	quiet := s.addTask(s.app, "0 0 * * *", "UTC")
	add := func(task *models.ScheduledTask, n int) {
		for range n {
			s.Require().NoError(facades.Orm().Query().Create(&models.ScheduledTaskRun{
				ScheduledTaskID: task.ID, Trigger: models.RunOnSchedule, Status: models.RunSucceeded,
				StartedAt: carbon.NewDateTime(carbon.Now()),
			}))
		}
	}
	add(busy, services.KeepTaskRuns+5)
	add(quiet, 3)
	newest := s.runs(busy)[services.KeepTaskRuns+4].ID

	s.Require().NoError(services.PruneTaskRuns(context.Background()))

	runs := s.runs(busy)
	s.Len(runs, services.KeepTaskRuns)
	s.Equal(newest, runs[len(runs)-1].ID)
	s.Equal(s.runs(busy)[0].ID, runs[0].ID)
	s.Len(s.runs(quiet), 3)
}

func (s *ScheduledTasksTestSuite) tab() string {
	return fmt.Sprintf("/apps/%d?tab=tasks", s.app.ID)
}

func (s *ScheduledTasksTestSuite) signedIn() *browser {
	b := signedIn(s.T(), &s.TestCase)
	b.visit(s.tab())

	return b
}

func (s *ScheduledTasksTestSuite) listed(b *browser) []map[string]any {
	_, page := b.visit(s.tab())
	rows := []map[string]any{}
	for _, row := range page.Props["scheduledTasks"].([]any) {
		rows = append(rows, row.(map[string]any))
	}

	return rows
}

func taskForm() map[string]any {
	return map[string]any{
		"name": " Laravel scheduler ", "schedule": " */5  * * * * ", "timezone": "Europe/London",
		"command": "php artisan schedule:run", "timeout_minutes": 15, "enabled": true,
	}
}

func (s *ScheduledTasksTestSuite) TestAddingATask() {
	b := s.signedIn()

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/tasks", s.app.ID), s.tab(), taskForm()).
		AssertHeader("Location", s.tab())

	var task models.ScheduledTask
	s.Require().NoError(facades.Orm().Query().Where("app_id", s.app.ID).FirstOrFail(&task))
	s.Equal("Laravel scheduler", task.Name)
	s.Equal("*/5 * * * *", task.Schedule, "stored with single spaces")
	s.Equal("Europe/London", task.Timezone)
	s.Equal(15, task.TimeoutMinutes)
	s.True(task.Enabled)

	_, page := b.visit(s.tab())
	s.Equal("Added Laravel scheduler.", page.Flash["success"])
	rows := s.listed(b)
	s.Require().Len(rows, 1)
	s.Equal("Laravel scheduler", rows[0]["name"])
	s.Equal(float64(15), rows[0]["timeout_minutes"])
	s.Nil(rows[0]["last_run"])
	next, err := time.Parse(time.RFC3339, rows[0]["next_run_at"].(string))
	s.Require().NoError(err)
	s.WithinDuration(time.Now(), next, 5*time.Minute+time.Second)
	s.Zero(next.Minute() % 5)
}

func (s *ScheduledTasksTestSuite) TestTheFormIsChecked() {
	b := s.signedIn()
	for _, tt := range []struct {
		field string
		value any
	}{
		{"name", ""},
		{"schedule", "@hourly"},
		{"schedule", "0 0 30 2 *"},
		{"timezone", "Local"},
		{"command", ""},
		{"timeout_minutes", 0},
		{"timeout_minutes", 1441},
	} {
		form := taskForm()
		form[tt.field] = tt.value
		b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/tasks", s.app.ID), s.tab(), form).
			AssertHeader("Location", s.tab())
		_, page := b.visit(s.tab())
		s.Contains(page.Props["errors"], tt.field, "%s = %v", tt.field, tt.value)
	}

	n, err := facades.Orm().Query().Model(&models.ScheduledTask{}).Count()
	s.Require().NoError(err)
	s.Zero(n)
}

func (s *ScheduledTasksTestSuite) TestEditingAndTurningOffATask() {
	task := s.addTask(s.app, "* * * * *", "UTC")
	b := s.signedIn()
	base := fmt.Sprintf("/apps/%d/tasks/%d", s.app.ID, task.ID)

	form := taskForm()
	form["name"] = "Nightly report"
	form["schedule"] = "0 2 * * *"
	form["timeout_minutes"] = 60
	b.submit(nethttp.MethodPut, base, s.tab(), form).AssertHeader("Location", s.tab())
	s.Require().NoError(facades.Orm().Query().Find(task, task.ID))
	s.Equal("Nightly report", task.Name)
	s.Equal("0 2 * * *", task.Schedule)
	s.Equal(60, task.TimeoutMinutes)

	b.submit(nethttp.MethodPut, base+"/enabled", s.tab(), map[string]any{"enabled": false}).AssertHeader("Location", s.tab())
	rows := s.listed(b)
	s.Equal(false, rows[0]["enabled"])
	s.Nil(rows[0]["next_run_at"], "no next run while it's off")

	b.submit(nethttp.MethodPut, base+"/enabled", s.tab(), map[string]any{"enabled": true})
	s.Equal(true, s.listed(b)[0]["enabled"])
}

func (s *ScheduledTasksTestSuite) TestDeletingATaskDeletesItsRuns() {
	s.succeed("", 0)
	task := s.addTask(s.app, "* * * * *", "UTC")
	s.runDue("2027-01-05 10:00")
	b := s.signedIn()

	b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d/tasks/%d", s.app.ID, task.ID), s.tab(), nil).
		AssertHeader("Location", s.tab())

	s.Empty(s.listed(b))
	n, err := facades.Orm().Query().Model(&models.ScheduledTaskRun{}).Count()
	s.Require().NoError(err)
	s.Zero(n)
}

func (s *ScheduledTasksTestSuite) TestRunNow() {
	release := make(chan struct{})
	s.docker.ExecToFunc = func(_ context.Context, _ string, _ []string, out io.Writer) (int, error) {
		<-release
		_, _ = io.WriteString(out, "done\n")
		return 0, nil
	}
	task := s.addTask(s.app, "0 0 1 1 *", "UTC")
	b := s.signedIn()
	run := fmt.Sprintf("/apps/%d/tasks/%d/run", s.app.ID, task.ID)

	b.submit(nethttp.MethodPost, run, s.tab(), nil).AssertHeader("Location", s.tab())
	_, page := b.visit(s.tab())
	s.Equal("Started Scheduler.", page.Flash["success"])
	last := s.listed(b)[0]["last_run"].(map[string]any)
	s.Equal("running", last["status"])
	s.Equal("manual", last["trigger"])

	b.submit(nethttp.MethodPost, run, s.tab(), nil)
	_, page = b.visit(s.tab())
	s.Equal("Scheduler is already running.", page.Flash["info"])

	close(release)
	services.WaitForTaskRuns()
	s.Equal("succeeded", s.listed(b)[0]["last_run"].(map[string]any)["status"])
}

func (s *ScheduledTasksTestSuite) TestRunsAndTheirOutputAreJSON() {
	s.succeed("first line\n", 0)
	task := s.addTask(s.app, "* * * * *", "UTC")
	other := s.addTask(s.app, "* * * * *", "UTC")
	s.runDue("2027-01-05 10:00")
	s.runDue("2027-01-05 10:01")
	b := s.signedIn()
	base := fmt.Sprintf("/apps/%d/tasks/%d", s.app.ID, task.ID)

	resp := b.request(nethttp.MethodGet, base+"/runs", nil, false, nil)
	resp.AssertOk()
	body, err := resp.Json()
	s.Require().NoError(err)
	runs := body["runs"].([]any)
	s.Require().Len(runs, 2)
	newest := runs[0].(map[string]any)
	s.Equal("succeeded", newest["status"])
	s.NotContains(newest, "output", "lists leave the output out")
	s.Greater(newest["id"], runs[1].(map[string]any)["id"], "newest first")

	id := uint(newest["id"].(float64))
	resp = b.request(nethttp.MethodGet, fmt.Sprintf("%s/runs/%d", base, id), nil, false, nil)
	resp.AssertOk()
	run, err := resp.Json()
	s.Require().NoError(err)
	s.Equal("first line\n", run["output"])

	b.request(nethttp.MethodGet, fmt.Sprintf("/apps/%d/tasks/%d/runs/%d", s.app.ID, other.ID, id), nil, false, nil).AssertNotFound()
}

func (s *ScheduledTasksTestSuite) TestTasksBelongToTheirCustomApp() {
	task := s.addTask(s.app, "* * * * *", "UTC")
	shop := createTestApp(s.T(), "shop", nil)
	tmpl, _ := templates.Find("redis")
	db, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "cache", Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)
	b := s.signedIn()

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/tasks", db.ID), s.tab(), taskForm()).AssertNotFound()
	_, page := b.visit(fmt.Sprintf("/apps/%d", db.ID))
	s.Empty(page.Props["scheduledTasks"], "a template app has none")
	s.Empty(page.Props["taskFailures"])

	wrong := fmt.Sprintf("/apps/%d/tasks/%d", shop.ID, task.ID)
	b.submit(nethttp.MethodPut, wrong, s.tab(), taskForm()).AssertNotFound()
	b.submit(nethttp.MethodDelete, wrong, s.tab(), nil).AssertNotFound()
	b.submit(nethttp.MethodPost, wrong+"/run", s.tab(), nil).AssertNotFound()
	b.request(nethttp.MethodGet, wrong+"/runs", nil, false, nil).AssertNotFound()
	s.Len(s.listed(b), 1)
}

func (s *ScheduledTasksTestSuite) TestPreviewListsTheNextRuns() {
	b := s.signedIn()

	resp := b.request(nethttp.MethodGet, "/scheduled-tasks/preview?schedule=0+9+*+*+1&timezone=America/New_York", nil, false, nil)
	resp.AssertOk()
	body, err := resp.Json()
	s.Require().NoError(err)
	s.Nil(body["error"])
	next := body["next"].([]any)
	s.Require().Len(next, 3)
	first, err := time.Parse(time.RFC3339, next[0].(string))
	s.Require().NoError(err)
	ny, _ := time.LoadLocation("America/New_York")
	s.Equal(time.Monday, first.In(ny).Weekday())
	s.Equal(9, first.In(ny).Hour())

	resp = b.request(nethttp.MethodGet, "/scheduled-tasks/preview?schedule=@hourly&timezone=UTC", nil, false, nil)
	body, err = resp.Json()
	s.Require().NoError(err)
	s.Contains(body["error"], "five fields")
	s.Empty(body["next"])
}

// addRun records a finished run of task, as the runner would.
func (s *ScheduledTasksTestSuite) addRun(task *models.ScheduledTask, status models.RunStatus, code int, output string) uint {
	now := carbon.NewDateTime(carbon.Now())
	run := &models.ScheduledTaskRun{
		ScheduledTaskID: task.ID, Trigger: models.RunOnSchedule, Status: status, ExitCode: &code,
		Output: output, StartedAt: now, FinishedAt: now,
	}
	s.Require().NoError(facades.Orm().Query().Create(run))

	return run.ID
}

// failures is the Overview's taskFailures prop.
func (s *ScheduledTasksTestSuite) failures(b *browser) []map[string]any {
	_, page := b.visit(fmt.Sprintf("/apps/%d", s.app.ID))
	rows := []map[string]any{}
	for _, row := range page.Props["taskFailures"].([]any) {
		rows = append(rows, row.(map[string]any))
	}

	return rows
}

func (s *ScheduledTasksTestSuite) dismiss(b *browser, task *models.ScheduledTask, run uint) contractstesting.Response {
	overview := fmt.Sprintf("/apps/%d", s.app.ID)
	uri := fmt.Sprintf("/apps/%d/tasks/%d/dismiss", s.app.ID, task.ID)

	return b.submit(nethttp.MethodPost, uri, overview, map[string]any{"run": run})
}

func runID(failure map[string]any) uint {
	return uint(failure["run"].(map[string]any)["id"].(float64))
}

func (s *ScheduledTasksTestSuite) TestAFailureShowsOnOverviewUntilDismissed() {
	task := s.addTask(s.app, "* * * * *", "UTC")
	s.addRun(task, models.RunSucceeded, 0, "fine\n")
	b := s.signedIn()
	s.Empty(s.failures(b))

	failed := s.addRun(task, models.RunFailed, 3, "one\ntwo\nthree\nfour\nfive\nsix\n")
	got := s.failures(b)
	s.Require().Len(got, 1)
	s.Equal("Scheduler", got[0]["task_name"])
	s.Equal(failed, runID(got[0]))
	s.Equal(float64(3), got[0]["run"].(map[string]any)["exit_code"])
	s.Equal("two\nthree\nfour\nfive\nsix", got[0]["tail"], "the last five lines")
	s.Nil(got[0]["succeeded_at"])

	s.addRun(task, models.RunSucceeded, 0, "")
	got = s.failures(b)
	s.Require().Len(got, 1, "a success since doesn't hide it")
	s.NotNil(got[0]["succeeded_at"])

	s.dismiss(b, task, failed).AssertHeader("Location", fmt.Sprintf("/apps/%d", s.app.ID))
	s.Empty(s.failures(b))

	timedOut := s.addRun(task, models.RunTimedOut, 0, "")
	got = s.failures(b)
	s.Require().Len(got, 1, "the next failure brings it back")
	s.Equal(timedOut, runID(got[0]))
	s.Equal("timed_out", got[0]["run"].(map[string]any)["status"])

	s.dismiss(b, task, failed)
	s.Len(s.failures(b), 1, "dismissing an older run leaves a newer failure")
	s.dismiss(b, task, timedOut)
	s.dismiss(b, task, failed)
	var stored models.ScheduledTask
	s.Require().NoError(facades.Orm().Query().Find(&stored, task.ID))
	s.Require().NotNil(stored.DismissedRunID)
	s.Equal(timedOut, *stored.DismissedRunID, "an older run never moves it back")
	s.Empty(s.failures(b))
}

func (s *ScheduledTasksTestSuite) TestSkippedAndInterruptedRunsRaiseNoAlert() {
	task := s.addTask(s.app, "* * * * *", "UTC")
	s.addRun(task, models.RunSkipped, 0, "")
	s.addRun(task, models.RunInterrupted, 0, "")
	b := s.signedIn()
	s.Empty(s.failures(b))

	failed := s.addRun(task, models.RunFailed, 1, "")
	s.addRun(task, models.RunSkipped, 0, "")
	got := s.failures(b)
	s.Require().Len(got, 1, "a skip after a failure doesn't hide it")
	s.Equal(failed, runID(got[0]))
	s.Empty(got[0]["tail"])
}

func (s *ScheduledTasksTestSuite) TestALongLastLineIsCut() {
	task := s.addTask(s.app, "* * * * *", "UTC")
	s.addRun(task, models.RunFailed, 1, strings.Repeat("é", 2000))
	b := s.signedIn()

	tail := s.failures(b)[0]["tail"].(string)
	s.True(strings.HasPrefix(tail, "…"))
	s.LessOrEqual(len(tail), 1024+len("…"))
	s.True(utf8.ValidString(tail))
}

func (s *ScheduledTasksTestSuite) TestDismissTakesOnlyThisTasksRuns() {
	task := s.addTask(s.app, "* * * * *", "UTC")
	other := s.addTask(s.app, "* * * * *", "UTC")
	failed := s.addRun(task, models.RunFailed, 1, "")
	theirs := s.addRun(other, models.RunFailed, 1, "")
	b := s.signedIn()

	s.dismiss(b, task, theirs).AssertNotFound()
	s.dismiss(b, task, 0).AssertNotFound()
	shop := createTestApp(s.T(), "shop", nil)
	uri := fmt.Sprintf("/apps/%d/tasks/%d/dismiss", shop.ID, task.ID)
	b.submit(nethttp.MethodPost, uri, s.tab(), map[string]any{"run": failed}).AssertNotFound()

	s.Len(s.failures(b), 2)
}
