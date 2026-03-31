package release

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	defaultMonitorWorkflow = "release.yml"
	defaultLogTailLines    = 400
	logScrollPageSize      = 12
	logFetchWorkers        = 2
)

type MonitorOptions struct {
	RunID        string
	Workflow     string
	PollInterval time.Duration
	ReleaseTag   string
}

type workflowRun struct {
	DatabaseID   int64         `json:"databaseId"`
	Status       string        `json:"status"`
	Conclusion   string        `json:"conclusion"`
	DisplayTitle string        `json:"displayTitle"`
	WorkflowName string        `json:"workflowName"`
	StartedAt    string        `json:"startedAt"`
	UpdatedAt    string        `json:"updatedAt"`
	URL          string        `json:"url"`
	Jobs         []workflowJob `json:"jobs"`
}

type workflowJob struct {
	DatabaseID  int64          `json:"databaseId"`
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	Conclusion  string         `json:"conclusion"`
	StartedAt   string         `json:"startedAt"`
	CompletedAt string         `json:"completedAt"`
	URL         string         `json:"url"`
	Steps       []workflowStep `json:"steps"`
}

type workflowStep struct {
	Number      int    `json:"number"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
}

type ghClient struct{}

type monitorModel struct {
	mu sync.RWMutex

	run              workflowRun
	selectedJob      int64
	logs             map[int64]string
	logErrors        map[int64]string
	logFetchInFlight map[int64]bool
	statusLine       string
	autoFollow       bool
	stepsAutoFollow  bool
	lastRefresh      time.Time
}

func RunMonitor(ctx context.Context, opts MonitorOptions) error {
	if opts.Workflow == "" {
		opts.Workflow = defaultMonitorWorkflow
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 3 * time.Second
	}

	client := ghClient{}
	runID := strings.TrimSpace(opts.RunID)
	if runID == "" {
		var err error
		runID, err = client.latestRunID(ctx, opts.Workflow)
		if err != nil {
			return err
		}
	}
	if runID == "" {
		return fmt.Errorf("no %s workflow runs found", opts.Workflow)
	}

	model := &monitorModel{
		logs:             make(map[int64]string),
		logErrors:        make(map[int64]string),
		logFetchInFlight: make(map[int64]bool),
		statusLine:       fmt.Sprintf("Using release workflow run: %s", runID),
		autoFollow:       true,
		stepsAutoFollow:  true,
	}

	app := tview.NewApplication()
	header := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	header.SetBorder(true).SetTitle(" Release ").SetTitleAlign(tview.AlignLeft)

	jobsList := tview.NewList().
		ShowSecondaryText(false).
		SetHighlightFullLine(true)
	jobsList.SetMainTextColor(tcell.ColorWhite).
		SetSelectedBackgroundColor(tcell.ColorDodgerBlue).
		SetSelectedTextColor(tcell.ColorBlack)
	jobsList.SetBorder(true).SetTitle(" Jobs ").SetTitleAlign(tview.AlignLeft)

	summaryView := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	summaryView.SetBorder(true).SetTitle(" Summary ").SetTitleAlign(tview.AlignLeft)

	metaView := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	metaView.SetBorder(true).SetTitle(" Selected Job ").SetTitleAlign(tview.AlignLeft)

	stepsView := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false).
		SetScrollable(true)
	stepsView.SetBorder(true).SetTitle(" Steps ").SetTitleAlign(tview.AlignLeft)

	logView := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false).
		SetScrollable(true)
	logView.SetBorder(true).SetTitle(" Log Tail ").SetTitleAlign(tview.AlignLeft)

	footer := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	footer.SetText("[gray]Mouse: jobs select  job pane open run  log pane open job  Enter/o open job  w open run")

	leftPane := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(jobsList, 0, 1, true).
		AddItem(summaryView, 4, 0, false)

	rightPane := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(metaView, 6, 0, false).
		AddItem(stepsView, 0, 2, false).
		AddItem(logView, 0, 3, false)

	mainPane := tview.NewFlex().
		AddItem(leftPane, 0, 2, true).
		AddItem(rightPane, 0, 3, false)

	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(header, 3, 0, false).
		AddItem(mainPane, 0, 1, true).
		AddItem(footer, 1, 0, false)

	runRefreshReq := make(chan string, 1)
	logFetchReq := make(chan int64, 16)
	var renderAll func()
	suppressJobChange := false

	triggerRunRefresh := func(reason string) {
		select {
		case runRefreshReq <- reason:
		default:
		}
	}

	queueLogFetch := func(jobID int64) {
		if !model.startLogFetch(jobID) {
			return
		}
		select {
		case logFetchReq <- jobID:
		default:
			model.finishLogFetch(jobID)
		}
	}

	openSelectedJob := func() {
		run, selectedJobID, _, _, _, _, _, _ := model.snapshot()
		job := selectJob(run, selectedJobID)
		if job.DatabaseID == 0 || strings.TrimSpace(job.URL) == "" {
			model.setStatusLine("No job URL available to open")
			renderAll()
			return
		}
		if err := openURL(job.URL); err != nil {
			model.setStatusLine(fmt.Sprintf("open job failed: %v", err))
		} else {
			model.setStatusLine(fmt.Sprintf("Opened job: %s", job.Name))
		}
		renderAll()
	}

	openRun := func() {
		run, _, _, _, _, _, _, _ := model.snapshot()
		if strings.TrimSpace(run.URL) == "" {
			model.setStatusLine("No run URL available to open")
			renderAll()
			return
		}
		if err := openURL(run.URL); err != nil {
			model.setStatusLine(fmt.Sprintf("open run failed: %v", err))
		} else {
			model.setStatusLine(fmt.Sprintf("Opened run %d", run.DatabaseID))
		}
		renderAll()
	}

	metaView.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftClick {
			openRun()
			return 0, nil
		}
		return action, event
	})

	stepsView.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		switch action {
		case tview.MouseLeftClick:
			openRun()
			return 0, nil
		case tview.MouseScrollUp, tview.MouseScrollDown:
			model.setStepsAutoFollow(false)
			return action, event
		}
		return action, event
	})

	logView.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftClick {
			openSelectedJob()
			return 0, nil
		}
		return action, event
	})

	renderAll = func() {
		run, selectedJobID, statusLine, autoFollow, stepsAutoFollow, logs, logErrors, lastRefresh := model.snapshot()
		selectedJob := selectJob(run, selectedJobID)

		header.SetText(renderMonitorHeader(opts.ReleaseTag, run, statusLine, lastRefresh))
		summaryView.SetText(renderRunSummary(run))
		metaView.SetText(renderJobMeta(selectedJob, autoFollow))
		stepsView.SetText(renderJobSteps(selectedJob))
		if stepsAutoFollow {
			stepsView.ScrollToBeginning()
			stepsView.ScrollToEnd()
		}
		logView.SetTitle(fmt.Sprintf(" Log Tail: %s ", safeJobName(selectedJob)))
		logView.SetText(renderJobLog(selectedJob, logs[selectedJob.DatabaseID], logErrors[selectedJob.DatabaseID]))
		if autoFollow {
			logView.ScrollToEnd()
		}

		suppressJobChange = true
		selectedIndex := rebuildJobsList(jobsList, run.Jobs)
		if selectedJob.DatabaseID != 0 {
			for i, job := range run.Jobs {
				if job.DatabaseID == selectedJob.DatabaseID {
					selectedIndex = i
					break
				}
			}
		}
		if selectedIndex >= 0 && jobsList.GetItemCount() > 0 {
			jobsList.SetCurrentItem(selectedIndex)
		}
		suppressJobChange = false
	}

	jobsList.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if suppressJobChange {
			return
		}
		model.selectJobByIndex(index)
		model.setStepsAutoFollow(true)
		renderAll()
		run, selectedJobID, _, _, _, _, _, _ := model.snapshot()
		job := selectJob(run, selectedJobID)
		queueLogFetch(job.DatabaseID)
	})

	scrollLog := func(delta int) {
		row, col := logView.GetScrollOffset()
		if delta > 0 {
			model.setAutoFollow(false)
		}
		newRow := row + delta
		if newRow < 0 {
			newRow = 0
		}
		logView.ScrollTo(newRow, col)
	}

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			app.Stop()
			return nil
		case tcell.KeyEnter:
			if jobsList.HasFocus() {
				openSelectedJob()
				return nil
			}
		case tcell.KeyTab:
			if jobsList.HasFocus() {
				app.SetFocus(logView)
			} else {
				app.SetFocus(jobsList)
			}
			return nil
		case tcell.KeyPgUp:
			if logView.HasFocus() {
				scrollLog(-logScrollPageSize)
				return nil
			}
		case tcell.KeyPgDn:
			if logView.HasFocus() {
				scrollLog(logScrollPageSize)
				return nil
			}
		case tcell.KeyUp:
			if logView.HasFocus() {
				scrollLog(-1)
				return nil
			}
		case tcell.KeyDown:
			if logView.HasFocus() {
				scrollLog(1)
				return nil
			}
		case tcell.KeyHome:
			if logView.HasFocus() {
				model.setAutoFollow(false)
				logView.ScrollToBeginning()
				return nil
			}
		case tcell.KeyEnd:
			if logView.HasFocus() {
				model.setAutoFollow(true)
				logView.ScrollToEnd()
				return nil
			}
		}

		if event.Key() == tcell.KeyRune {
			switch event.Rune() {
			case 'q':
				app.Stop()
				return nil
			case 'o':
				openSelectedJob()
				return nil
			case 'r':
				triggerRunRefresh("manual")
				run, selectedJobID, _, _, _, _, _, _ := model.snapshot()
				queueLogFetch(selectJob(run, selectedJobID).DatabaseID)
				return nil
			case 'f':
				model.setAutoFollow(true)
				logView.ScrollToEnd()
				renderAll()
				return nil
			case 'w':
				openRun()
				return nil
			}
		}
		return event
	})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		ticker := time.NewTicker(opts.PollInterval)
		defer ticker.Stop()

		triggerRunRefresh("initial")

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				triggerRunRefresh("poll")
			case reason := <-runRefreshReq:
				run, err := client.run(ctx, runID)
				if err != nil {
					model.setStatusLine(fmt.Sprintf("refresh failed (%s): %v", reason, err))
					app.QueueUpdateDraw(renderAll)
					continue
				}

				selectedJob := model.updateRun(run)
				logText, logErr := client.jobLog(ctx, selectedJob.DatabaseID)
				model.updateLog(selectedJob.DatabaseID, logText, logErr)

				status := fmt.Sprintf("Run %s • %s", runID, decorateRunState(run.Status, run.Conclusion))
				if reason == "manual" {
					status += " • refreshed"
				}
				model.setStatusLine(status)
				app.QueueUpdateDraw(renderAll)

				for _, jobID := range candidateLogJobs(run, selectedJob.DatabaseID) {
					queueLogFetch(jobID)
				}
			}
		}
	}()

	for i := 0; i < logFetchWorkers; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case jobID := <-logFetchReq:
					logText, logErr := client.jobLog(ctx, jobID)
					model.updateLog(jobID, logText, logErr)
					model.finishLogFetch(jobID)
					app.QueueUpdateDraw(renderAll)
				}
			}
		}()
	}

	renderAll()
	app.EnableMouse(true)
	if err := app.SetRoot(layout, true).SetFocus(jobsList).Run(); err != nil {
		return err
	}
	return nil
}

func (c ghClient) latestRunID(ctx context.Context, workflow string) (string, error) {
	var out []struct {
		DatabaseID int64 `json:"databaseId"`
	}
	if err := c.runJSON(ctx, &out, "run", "list", "--workflow", workflow, "--limit", "1", "--json", "databaseId"); err != nil {
		return "", err
	}
	if len(out) == 0 || out[0].DatabaseID == 0 {
		return "", nil
	}
	return fmt.Sprintf("%d", out[0].DatabaseID), nil
}

func (c ghClient) run(ctx context.Context, runID string) (workflowRun, error) {
	var out workflowRun
	err := c.runJSON(ctx, &out, "run", "view", runID, "--json", "databaseId,status,conclusion,displayTitle,workflowName,startedAt,updatedAt,url,jobs")
	return out, err
}

func (c ghClient) jobLog(ctx context.Context, jobID int64) (string, error) {
	if jobID == 0 {
		return "", nil
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "gh", "run", "view", "--log", "--job", fmt.Sprintf("%d", jobID)) //nolint:gosec // trusted repo tool invocation
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return tailLines(string(out), defaultLogTailLines), nil
}

func (c ghClient) runJSON(ctx context.Context, target interface{}, args ...string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "gh", args...) //nolint:gosec // trusted repo tool invocation
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("gh %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return err
	}
	if err := json.Unmarshal(out, target); err != nil {
		return fmt.Errorf("parse gh output: %w", err)
	}
	return nil
}

func (m *monitorModel) snapshot() (workflowRun, int64, string, bool, bool, map[int64]string, map[int64]string, time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	logs := make(map[int64]string, len(m.logs))
	for k, v := range m.logs {
		logs[k] = v
	}
	logErrors := make(map[int64]string, len(m.logErrors))
	for k, v := range m.logErrors {
		logErrors[k] = v
	}
	return m.run, m.selectedJob, m.statusLine, m.autoFollow, m.stepsAutoFollow, logs, logErrors, m.lastRefresh
}

func (m *monitorModel) updateRun(run workflowRun) workflowJob {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.run = run
	m.lastRefresh = time.Now()
	if len(run.Jobs) == 0 {
		m.selectedJob = 0
		return workflowJob{}
	}

	for _, job := range run.Jobs {
		if job.DatabaseID == m.selectedJob {
			return job
		}
	}

	for _, job := range run.Jobs {
		if job.Status == "in_progress" {
			m.selectedJob = job.DatabaseID
			return job
		}
	}

	m.selectedJob = run.Jobs[0].DatabaseID
	return run.Jobs[0]
}

func (m *monitorModel) selectJobByIndex(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index >= len(m.run.Jobs) {
		return
	}
	m.selectedJob = m.run.Jobs[index].DatabaseID
}

func (m *monitorModel) updateLog(jobID int64, text string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if jobID == 0 {
		return
	}
	if err != nil {
		m.logErrors[jobID] = err.Error()
		return
	}
	m.logs[jobID] = text
	delete(m.logErrors, jobID)
}

func (m *monitorModel) startLogFetch(jobID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if jobID == 0 || m.logFetchInFlight[jobID] {
		return false
	}
	m.logFetchInFlight[jobID] = true
	return true
}

func (m *monitorModel) finishLogFetch(jobID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.logFetchInFlight, jobID)
}

func (m *monitorModel) setStatusLine(line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusLine = line
}

func (m *monitorModel) setAutoFollow(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.autoFollow = enabled
}

func (m *monitorModel) setStepsAutoFollow(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stepsAutoFollow = enabled
}

func selectJob(run workflowRun, selectedJobID int64) workflowJob {
	for _, job := range run.Jobs {
		if job.DatabaseID == selectedJobID {
			return job
		}
	}
	if len(run.Jobs) > 0 {
		return run.Jobs[0]
	}
	return workflowJob{}
}

func rebuildJobsList(list *tview.List, jobs []workflowJob) int {
	current := list.GetCurrentItem()
	list.Clear()
	for _, job := range jobs {
		list.AddItem(renderJobListItem(job), "", 0, nil)
	}
	if current >= list.GetItemCount() {
		current = list.GetItemCount() - 1
	}
	if current < 0 {
		current = 0
	}
	return current
}

func renderMonitorHeader(releaseTag string, run workflowRun, statusLine string, lastRefresh time.Time) string {
	label := releaseTag
	if label == "" {
		label = run.WorkflowName
	}
	updated := parseGitHubTime(run.UpdatedAt)
	updatedText := "n/a"
	if !updated.IsZero() {
		updatedText = updated.Local().Format("15:04:05")
	}
	refreshText := "waiting"
	if !lastRefresh.IsZero() {
		refreshText = lastRefresh.Local().Format("15:04:05")
	}
	return fmt.Sprintf(
		"[white::b]%s[::-]  Run [white::b]%d[::-]  %s  updated [white]%s[::-]  refreshed [white]%s[::-]\n[gray]%s",
		label,
		run.DatabaseID,
		decorateRunState(run.Status, run.Conclusion),
		updatedText,
		refreshText,
		statusLine,
	)
}

func renderRunSummary(run workflowRun) string {
	if len(run.Jobs) == 0 {
		return "[yellow]Loading run data...\n[gray]Waiting for GitHub Actions job list."
	}

	var running, completed, failed, pending int
	for _, job := range run.Jobs {
		switch {
		case job.Status == "in_progress":
			running++
		case job.Conclusion == "failure":
			failed++
		case job.Status == "completed":
			completed++
		default:
			pending++
		}
	}

	return fmt.Sprintf(
		"%d total\n[green]%d complete[white]\n[yellow]%d running[white]\n[red]%d failed[white]\n[gray]%d pending[white]",
		len(run.Jobs),
		completed,
		running,
		failed,
		pending,
	)
}

func renderJobMeta(job workflowJob, autoFollow bool) string {
	if job.DatabaseID == 0 {
		follow := "off"
		if autoFollow {
			follow = "on"
		}
		return fmt.Sprintf("[yellow]Loading selected job...\n[gray]Run resolved. Waiting for jobs and steps.\nLog follow: %s", follow)
	}

	started := parseGitHubTime(job.StartedAt)
	completed := parseGitHubTime(job.CompletedAt)
	duration := formatDurationBetween(started, completed)
	if duration == "" {
		duration = "n/a"
	}

	follow := "off"
	if autoFollow {
		follow = "on"
	}

	return fmt.Sprintf(
		"[white::b]%s[::-]\nStatus: %s\nDuration: %s\nStarted: %s\nLog follow: %s\n\n[gray]%s",
		job.Name,
		decorateRunState(job.Status, job.Conclusion),
		duration,
		formatTimeForView(started),
		follow,
		job.URL,
	)
}

func renderJobSteps(job workflowJob) string {
	if job.DatabaseID == 0 {
		return "[gray]Waiting for step data from GitHub Actions..."
	}
	if len(job.Steps) == 0 {
		return "[gray]No step data available yet."
	}

	var lines []string
	for _, step := range job.Steps {
		lines = append(lines, fmt.Sprintf("%2d  %-42s %s", step.Number, truncateString(step.Name, 42), decorateRunState(step.Status, step.Conclusion)))
	}
	return strings.Join(lines, "\n")
}

func renderJobLog(job workflowJob, logText, logErr string) string {
	if job.DatabaseID == 0 {
		return "[gray]Waiting for selected job logs..."
	}
	if strings.TrimSpace(logText) != "" {
		return logText
	}
	if logErr != "" {
		return fmt.Sprintf("[yellow]Log unavailable:[white] %s", logErr)
	}
	if job.Status == "completed" {
		return "[gray]No job log output returned."
	}
	return "[gray]Waiting for job log output..."
}

func renderJobListItem(job workflowJob) string {
	return fmt.Sprintf("%s %s (%s)", jobMarker(job), safeJobName(job), formatDurationBetween(parseGitHubTime(job.StartedAt), parseGitHubTime(job.CompletedAt)))
}

func jobMarker(job workflowJob) string {
	switch {
	case job.Conclusion == "success":
		return "[ok]"
	case job.Conclusion == "failure":
		return "[fail]"
	case job.Status == "in_progress":
		return "[run]"
	default:
		return "[wait]"
	}
}

func safeJobName(job workflowJob) string {
	name := strings.TrimSpace(job.Name)
	if name != "" {
		return name
	}
	if job.DatabaseID != 0 {
		return fmt.Sprintf("job-%d", job.DatabaseID)
	}
	return "(unnamed job)"
}

func decorateRunState(status, conclusion string) string {
	switch {
	case conclusion == "success":
		return "[green]success[white]"
	case conclusion == "failure":
		return "[red]failure[white]"
	case conclusion == "cancelled":
		return "[yellow]cancelled[white]"
	case status == "in_progress":
		return "[yellow]in_progress[white]"
	case status == "completed" && conclusion == "":
		return "[gray]completed[white]"
	case status != "":
		return fmt.Sprintf("[gray]%s[white]", status)
	default:
		return "[gray]unknown[white]"
	}
}

func parseGitHubTime(value string) time.Time {
	if value == "" || strings.HasPrefix(value, "0001-01-01") {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func formatDurationBetween(startedAt, completedAt time.Time) string {
	if startedAt.IsZero() {
		return "n/a"
	}
	end := completedAt
	if end.IsZero() {
		end = time.Now()
	}
	d := end.Sub(startedAt)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func formatTimeForView(ts time.Time) string {
	if ts.IsZero() {
		return "n/a"
	}
	return ts.Local().Format("2006-01-02 15:04:05")
}

func truncateString(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func tailLines(value string, maxLines int) string {
	if maxLines <= 0 {
		return value
	}

	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	if len(lines) <= maxLines {
		return strings.TrimRight(value, "\n")
	}
	lines = lines[len(lines)-maxLines:]
	return "[gray](showing last log lines)\n[white]" + strings.Join(lines, "\n")
}

func candidateLogJobs(run workflowRun, selectedJobID int64) []int64 {
	seen := make(map[int64]bool)
	var jobs []int64
	if selectedJobID != 0 {
		seen[selectedJobID] = true
		jobs = append(jobs, selectedJobID)
	}
	for _, job := range run.Jobs {
		if job.DatabaseID == 0 || seen[job.DatabaseID] {
			continue
		}
		if job.Status == "in_progress" {
			seen[job.DatabaseID] = true
			jobs = append(jobs, job.DatabaseID)
		}
	}
	return jobs
}

func openURL(rawURL string) error {
	url := strings.TrimSpace(rawURL)
	if url == "" {
		return fmt.Errorf("empty url")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url) //nolint:gosec // trusted local opener invocation
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec // trusted local opener invocation
	default:
		cmd = exec.Command("xdg-open", url) //nolint:gosec // trusted local opener invocation
	}
	return cmd.Start()
}
