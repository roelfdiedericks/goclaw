package cron

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/delivery"
)

type deliveryGatewayStub struct {
	ownerUserID   string
	lastAssistant *delivery.AssistantMessage
	lastSystem    *delivery.SystemMessage
	lastHandoff   string
	report        delivery.Report
}

func (d *deliveryGatewayStub) RunAgentForCron(ctx context.Context, req AgentRequest, events chan<- AgentEvent) {
	events <- AgentEndEvent{FinalText: "assistant path result"}
	close(events)
}

func (d *deliveryGatewayStub) GetOwnerUserID() string {
	return d.ownerUserID
}

func (d *deliveryGatewayStub) InjectSystemEvent(ctx context.Context, text string) error {
	return nil
}

func (d *deliveryGatewayStub) DeliverAssistantOutput(ctx context.Context, userID string, msg delivery.AssistantMessage) delivery.Report {
	copyMsg := msg
	d.lastAssistant = &copyMsg
	return d.report
}

func (d *deliveryGatewayStub) DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report {
	copyMsg := msg
	d.lastSystem = &copyMsg
	return d.report
}

func (d *deliveryGatewayStub) HandoffCronResult(ctx context.Context, jobName, result string) error {
	d.lastHandoff = jobName + ":" + result
	return nil
}

func TestDeliverAssistantOutputUsesGatewayDeliverySeam(t *testing.T) {
	gw := &deliveryGatewayStub{
		ownerUserID: "owner",
		report:      delivery.Report{Generated: true, DeliveredTo: 1},
	}
	svc := &Service{gateway: gw}

	svc.deliverAssistantOutput(context.Background(), "cron", "Hello world", true)

	if gw.lastAssistant == nil {
		t.Fatalf("expected assistant delivery to be sent to gateway")
	}
	if gw.lastAssistant.Source != "cron" {
		t.Fatalf("expected source cron, got %q", gw.lastAssistant.Source)
	}
	if !gw.lastAssistant.Persist {
		t.Fatalf("expected cron assistant delivery to request persistence")
	}
	if gw.lastAssistant.PersistKind != "delivered" {
		t.Fatalf("expected persist kind delivered, got %q", gw.lastAssistant.PersistKind)
	}
	if gw.lastAssistant.Content != "Hello world" {
		t.Fatalf("expected raw assistant content, got %q", gw.lastAssistant.Content)
	}
	if gw.lastAssistant.PersistContent != "Hello world" {
		t.Fatalf("expected raw persist content, got %q", gw.lastAssistant.PersistContent)
	}
}

func TestHandleResultStoreOnlySkipsDelivery(t *testing.T) {
	gw := &deliveryGatewayStub{ownerUserID: "owner"}
	svc := &Service{gateway: gw}
	job := &CronJob{
		Name:   "quiet",
		Prompt: "check something",
		Result: ResultPolicy{Mode: ResultModeStoreOnly},
	}

	if err := svc.handleResult(context.Background(), job, "stored only"); err != nil {
		t.Fatalf("handleResult returned error: %v", err)
	}
	if gw.lastAssistant != nil {
		t.Fatalf("expected no assistant delivery for store_only")
	}
	if gw.lastHandoff != "" {
		t.Fatalf("expected no handoff for store_only")
	}
}

func TestHandleResultDeliverUsesAssistantSurface(t *testing.T) {
	gw := &deliveryGatewayStub{
		ownerUserID: "owner",
		report:      delivery.Report{Generated: true, DeliveredTo: 1},
	}
	svc := &Service{gateway: gw}
	job := &CronJob{
		Name:   "deliver me",
		Prompt: "say hi",
		Result: ResultPolicy{Mode: ResultModeDeliver},
	}

	if err := svc.handleResult(context.Background(), job, "hello"); err != nil {
		t.Fatalf("handleResult returned error: %v", err)
	}
	if gw.lastAssistant == nil {
		t.Fatalf("expected assistant delivery for deliver mode")
	}
	if !gw.lastAssistant.Persist {
		t.Fatalf("expected smart-default persistence for deliver mode")
	}
	if gw.lastHandoff != "" {
		t.Fatalf("expected no handoff for deliver mode")
	}
}

func TestHandleResultHandoffUsesMainAgentPath(t *testing.T) {
	gw := &deliveryGatewayStub{ownerUserID: "owner"}
	svc := &Service{gateway: gw}
	job := &CronJob{
		Name:   "handoff me",
		Prompt: "summarize",
		Result: ResultPolicy{Mode: ResultModeHandoffMain},
	}

	if err := svc.handleResult(context.Background(), job, "result text"); err != nil {
		t.Fatalf("handleResult returned error: %v", err)
	}
	if gw.lastHandoff != "handoff me:result text" {
		t.Fatalf("expected handoff result, got %q", gw.lastHandoff)
	}
	if gw.lastAssistant != nil {
		t.Fatalf("expected no assistant delivery for handoff_main")
	}
}

func TestShouldPersistResultSmartDefaultsAndOverride(t *testing.T) {
	falseVal := false

	tests := []struct {
		name string
		job  CronJob
		want bool
	}{
		{
			name: "store only defaults to persist",
			job:  CronJob{Result: ResultPolicy{Mode: ResultModeStoreOnly}},
			want: true,
		},
		{
			name: "deliver defaults to persist",
			job:  CronJob{Result: ResultPolicy{Mode: ResultModeDeliver}},
			want: true,
		},
		{
			name: "handoff defaults to persist",
			job:  CronJob{Result: ResultPolicy{Mode: ResultModeHandoffMain}},
			want: true,
		},
		{
			name: "explicit none disables persistence",
			job:  CronJob{Result: ResultPolicy{Mode: ResultModeDeliver, Persist: &falseVal}},
			want: false,
		},
	}

	for _, tc := range tests {
		if got := tc.job.ShouldPersistResult(); got != tc.want {
			t.Fatalf("%s: ShouldPersistResult() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCronJobValidateRequiresPromptAndValidResultMode(t *testing.T) {
	job := &CronJob{Name: "bad", Prompt: "", Result: ResultPolicy{Mode: ResultModeDeliver}}
	if err := job.Validate(); err == nil {
		t.Fatalf("expected missing prompt to fail validation")
	}

	job = &CronJob{Name: "bad", Prompt: "do something", Result: ResultPolicy{Mode: ResultMode("weird")}}
	if err := job.Validate(); err == nil {
		t.Fatalf("expected invalid result mode to fail validation")
	}
}

func TestNextRunChanged(t *testing.T) {
	now := time.Unix(1700000000, 0)
	nowMs := now.UnixMilli()

	tests := []struct {
		name    string
		current *int64
		next    *time.Time
		want    bool
	}{
		{name: "both nil", current: nil, next: nil, want: false},
		{name: "current nil next set", current: nil, next: &now, want: true},
		{name: "current set next nil", current: &nowMs, next: nil, want: true},
		{name: "same timestamp", current: &nowMs, next: &now, want: false},
		{name: "different timestamp", current: &nowMs, next: ptrTime(now.Add(time.Minute)), want: true},
	}

	for _, tc := range tests {
		if got := nextRunChanged(tc.current, tc.next); got != tc.want {
			t.Fatalf("%s: nextRunChanged() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

type delegatedDeliveryRunnerStub struct {
	startCalls int
	runID      string
	result     delegatedrun.RunResult
	state      delegatedrun.RunState
}

func (r *delegatedDeliveryRunnerStub) Start(ctx context.Context, spec delegatedrun.RunSpec) (string, error) {
	r.startCalls++
	if r.runID != "" {
		return r.runID, nil
	}
	return "delegated-test-run", nil
}

func (r *delegatedDeliveryRunnerStub) Cancel(runID string) error { return nil }

func (r *delegatedDeliveryRunnerStub) Wait(ctx context.Context, runID string) (delegatedrun.RunResult, delegatedrun.RunState, error) {
	return r.result, r.state, nil
}

type delegatedRunnerNoUseStub struct {
	startCalls int
}

func (r *delegatedRunnerNoUseStub) Start(ctx context.Context, spec delegatedrun.RunSpec) (string, error) {
	r.startCalls++
	return "unused", nil
}
func (r *delegatedRunnerNoUseStub) Cancel(runID string) error { return nil }
func (r *delegatedRunnerNoUseStub) Wait(ctx context.Context, runID string) (delegatedrun.RunResult, delegatedrun.RunState, error) {
	return delegatedrun.RunResult{}, delegatedrun.RunStateCompleted, nil
}

func TestExecuteJobDelegatedPathPreservesResultSemantics(t *testing.T) {
	tests := []struct {
		name            string
		mode            ResultMode
		expectAssistant bool
		expectHandoff   bool
	}{
		{name: "store_only stays silent", mode: ResultModeStoreOnly, expectAssistant: false, expectHandoff: false},
		{name: "deliver uses assistant surface", mode: ResultModeDeliver, expectAssistant: true, expectHandoff: false},
		{name: "handoff_main uses handoff path", mode: ResultModeHandoffMain, expectAssistant: false, expectHandoff: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			store := NewStore(filepath.Join(tmp, "jobs.json"), filepath.Join(tmp, "runs"))
			job := &CronJob{
				ID:      "job-1",
				Name:    "delegated-job",
				Enabled: true,
				Schedule: Schedule{
					Kind:    ScheduleKindEvery,
					EveryMs: 60_000,
				},
				Prompt: "run delegated",
				Result: ResultPolicy{Mode: tc.mode},
			}
			job.SetRunning()
			store.jobs[job.ID] = job

			gw := &deliveryGatewayStub{
				ownerUserID: "owner",
				report:      delivery.Report{Generated: true, DeliveredTo: 1},
			}
			runner := &delegatedDeliveryRunnerStub{
				runID:  "delegated-test-run",
				result: delegatedrun.RunResult{FinalText: "delegated final text"},
				state:  delegatedrun.RunStateCompleted,
			}
			svc := &Service{
				store:           store,
				history:         NewHistoryManager(filepath.Join(tmp, "runs")),
				gateway:         gw,
				runner:          runner,
				registry:        delegatedrun.NewMemoryRegistry(),
				delegatedEnabled: true,
			}

			svc.executeJob(context.Background(), job)

			if runner.startCalls != 1 {
				t.Fatalf("expected delegated runner Start to be called once, got %d", runner.startCalls)
			}

			if tc.expectAssistant {
				if gw.lastAssistant == nil {
					t.Fatalf("expected assistant delivery for mode %s", tc.mode)
				}
				if gw.lastAssistant.Content != "delegated final text" {
					t.Fatalf("expected delegated final text to be delivered, got %q", gw.lastAssistant.Content)
				}
			} else if gw.lastAssistant != nil {
				t.Fatalf("expected no assistant delivery for mode %s", tc.mode)
			}

			if tc.expectHandoff {
				if gw.lastHandoff != "delegated-job:delegated final text" {
					t.Fatalf("expected delegated handoff, got %q", gw.lastHandoff)
				}
			} else if gw.lastHandoff != "" {
				t.Fatalf("expected no handoff for mode %s, got %q", tc.mode, gw.lastHandoff)
			}
		})
	}
}

func TestExecuteJobWithDelegatedDisabledUsesAssistantPath(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(filepath.Join(tmp, "jobs.json"), filepath.Join(tmp, "runs"))
	job := &CronJob{
		ID:      "job-1",
		Name:    "assistant-fallback",
		Enabled: true,
		Schedule: Schedule{
			Kind:    ScheduleKindEvery,
			EveryMs: 60_000,
		},
		Prompt: "run assistant",
		Result: ResultPolicy{Mode: ResultModeDeliver},
	}
	job.SetRunning()
	store.jobs[job.ID] = job

	gw := &deliveryGatewayStub{
		ownerUserID: "owner",
		report:      delivery.Report{Generated: true, DeliveredTo: 1},
	}
	runner := &delegatedRunnerNoUseStub{}
	svc := &Service{
		store:            store,
		history:          NewHistoryManager(filepath.Join(tmp, "runs")),
		gateway:          gw,
		runner:           runner,
		delegatedEnabled: false,
	}

	svc.executeJob(context.Background(), job)

	if runner.startCalls != 0 {
		t.Fatalf("expected delegated runner to remain unused when feature flag disabled, got %d starts", runner.startCalls)
	}
	if gw.lastAssistant == nil {
		t.Fatalf("expected assistant delivery from non-delegated path")
	}
}

func TestRunNowDetachesFromCallerContext(t *testing.T) {
	store := &Store{
		jobs: map[string]*CronJob{
			"job-1": {
				ID:      "job-1",
				Name:    "detached",
				Enabled: true,
				Schedule: Schedule{
					Kind:    ScheduleKindEvery,
					EveryMs: 60000,
				},
				Prompt: "run detached",
				Result: ResultPolicy{Mode: ResultModeStoreOnly},
			},
		},
	}

	ctxCh := make(chan context.Context, 1)
	svc := &Service{store: store}
	svc.execJob = func(ctx context.Context, job *CronJob) {
		ctxCh <- ctx
	}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := svc.RunNow(cancelledCtx, "job-1"); err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}

	select {
	case runCtx := <-ctxCh:
		if runCtx.Err() != nil {
			t.Fatalf("expected detached run context, got canceled context: %v", runCtx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for executeJob to be invoked")
	}
}
