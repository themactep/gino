package cron

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSchedulerPersistence(t *testing.T) {
	dir := t.TempDir()
	jobsFile := filepath.Join(dir, "jobs.json")

	// Create scheduler, add jobs, persist.
	s1 := NewScheduler(nil)
	if err := s1.SetPersistencePath(jobsFile); err != nil {
		t.Fatalf("SetPersistencePath: %v", err)
	}
	s1.Add("one-time", "do something", 1*time.Hour, "telegram", "1")
	s1.AddRecurring("recurring", "check stuff", 30*time.Minute, "telegram", "2")

	// Simulate restart: new scheduler loads from same file.
	s2 := NewScheduler(nil)
	if err := s2.SetPersistencePath(jobsFile); err != nil {
		t.Fatalf("reload SetPersistencePath: %v", err)
	}

	jobs := s2.List()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 restored jobs, got %d", len(jobs))
	}

	names := map[string]bool{}
	for _, j := range jobs {
		names[j.Name] = true
	}
	if !names["one-time"] || !names["recurring"] {
		t.Errorf("expected both 'one-time' and 'recurring' jobs, got %v", names)
	}
}

func TestSchedulerPersistenceDropsExpired(t *testing.T) {
	dir := t.TempDir()
	jobsFile := filepath.Join(dir, "jobs.json")

	// Add a job that's already due.
	s1 := NewScheduler(nil)
	_ = s1.SetPersistencePath(jobsFile)
	s1.Add("past-job", "expired", -1*time.Second, "telegram", "1")
	// Manually close done channel pattern not needed — just reload.

	s2 := NewScheduler(nil)
	if err := s2.SetPersistencePath(jobsFile); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// The expired job should have been loaded (save happens on Add before FireAt check).
	// After loadLocked, expired one-time jobs are dropped.
	jobs := s2.List()
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs after reload (expired dropped), got %d", len(jobs))
	}
}

func TestSchedulerPersistenceRecurringReschedules(t *testing.T) {
	dir := t.TempDir()
	jobsFile := filepath.Join(dir, "jobs.json")

	s1 := NewScheduler(nil)
	_ = s1.SetPersistencePath(jobsFile)
	// Add recurring job with short interval.
	s1.AddRecurring("every-2m", "ping", 2*time.Minute, "telegram", "1")

	// Reload.
	s2 := NewScheduler(nil)
	if err := s2.SetPersistencePath(jobsFile); err != nil {
		t.Fatalf("reload: %v", err)
	}

	jobs := s2.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 recurring job, got %d", len(jobs))
	}
	if !jobs[0].Recurring {
		t.Error("expected job to be recurring")
	}
	// FireAt should be in the future (rescheduled from now).
	if !jobs[0].FireAt.After(time.Now()) {
		t.Error("expected recurring job FireAt to be in the future after reload")
	}
}

func TestSchedulerPersistenceFileFormat(t *testing.T) {
	dir := t.TempDir()
	jobsFile := filepath.Join(dir, "jobs.json")

	s1 := NewScheduler(nil)
	_ = s1.SetPersistencePath(jobsFile)
	s1.Add("test-job", "hello world", 5*time.Minute, "telegram", "99")

	// Verify file exists and is valid JSON with expected fields.
	data, err := os.ReadFile(jobsFile)
	if err != nil {
		t.Fatalf("read jobs file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("jobs file is empty")
	}
	// Should contain the job name and message.
	str := string(data)
	if !contains(str, "test-job") {
		t.Error("jobs file should contain job name")
	}
	if !contains(str, "hello world") {
		t.Error("jobs file should contain job message")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && (s[0:len(substr)] == substr || contains(s[1:], substr)))
}

func TestSchedulerFiresAfterReload(t *testing.T) {
	dir := t.TempDir()
	jobsFile := filepath.Join(dir, "jobs.json")

	// Phase 1: create and add a short job, let it save.
	s1 := NewScheduler(nil)
	_ = s1.SetPersistencePath(jobsFile)
	s1.Add("quick", "fire fast", 50*time.Millisecond, "telegram", "1")
	// Don't start the scheduler — just persist.
	time.Sleep(100 * time.Millisecond) // let it expire on disk without firing

	// Phase 2: reload — the job should be expired and dropped.
	var mu sync.Mutex
	var fired []Job
	s2 := NewScheduler(func(job Job) {
		mu.Lock()
		fired = append(fired, job)
		mu.Unlock()
	})
	if err := s2.SetPersistencePath(jobsFile); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// The expired one-time job should not fire.
	done := make(chan struct{})
	go s2.Start(done)
	time.Sleep(500 * time.Millisecond)
	close(done)

	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 0 {
		t.Errorf("expected 0 fired jobs (expired), got %d", len(fired))
	}
}

func TestSchedulerCronPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron_jobs.json")

	s1 := NewScheduler(func(j Job) {})
	s1.SetPersistencePath(path)

	// Add a cron job with a valid expression that fires soon.
	// Use "*/1 * * * *" (every minute) so it always has a near-future fire time.
	id, err := s1.AddScheduled("market-check", "Check stocks", "*/1 * * * *", "America/New_York", "telegram", "123")
	if err != nil {
		t.Fatalf("AddScheduled failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty job ID")
	}

	// Verify it was saved.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(data, []byte("market-check")) {
		t.Error("saved file does not contain job name")
	}
	if !bytes.Contains(data, []byte("*/1 * * * *")) {
		t.Error("saved file does not contain cron expression")
	}
	if !bytes.Contains(data, []byte("America/New_York")) {
		t.Error("saved file does not contain timezone")
	}

	// Simulate restart: load into a new scheduler.
	s2 := NewScheduler(func(j Job) {})
	s2.SetPersistencePath(path)
	jobs := s2.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job after reload, got %d", len(jobs))
	}
	if jobs[0].Name != "market-check" {
		t.Errorf("expected name 'market-check', got %q", jobs[0].Name)
	}
	if jobs[0].Schedule != "*/1 * * * *" {
		t.Errorf("expected schedule '*/1 * * * *', got %q", jobs[0].Schedule)
	}
	if jobs[0].Timezone != "America/New_York" {
		t.Errorf("expected timezone 'America/New_York', got %q", jobs[0].Timezone)
	}

	// Verify fire time was recomputed (should be in the near future).
	remaining := time.Until(jobs[0].FireAt)
	if remaining > 2*time.Minute {
		t.Errorf("cron job fire time too far out: %v", remaining)
	}
}

func TestSchedulerCronFiresAndReschedules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron_jobs.json")

	fired := make(chan Job, 10)
	s := NewScheduler(func(j Job) {
		fired <- j
	})
	s.SetPersistencePath(path)

	// Schedule a cron job that fires quickly.
	// "* * * * *" = every minute, so the first fire is within 60 seconds.
	// Use a short-lived approach: we'll use a 2-second tick and verify the job fires.
	_, err := s.AddScheduled("quick-cron", "fire!", "* * * * *", "UTC", "telegram", "123")
	if err != nil {
		t.Fatalf("AddScheduled: %v", err)
	}

	done := make(chan struct{})
	go s.Start(done)
	defer close(done)

	// Wait up to 90 seconds for the job to fire.
	select {
	case job := <-fired:
		if job.Name != "quick-cron" {
			t.Errorf("expected 'quick-cron', got %q", job.Name)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("cron job did not fire within 90 seconds")
	}

	// Verify the job was rescheduled (still in list).
	jobs := s.List()
	found := false
	for _, j := range jobs {
		if j.Name == "quick-cron" {
			found = true
			// Verify FireAt was moved forward.
			if !j.FireAt.After(time.Now()) {
				t.Error("cron job FireAt not moved forward after firing")
			}
		}
	}
	if !found {
		t.Error("cron job was removed after firing instead of rescheduling")
	}
}

func TestSchedulerCronInvalidExpression(t *testing.T) {
	s := NewScheduler(func(j Job) {})

	_, err := s.AddScheduled("bad", "test", "invalid expression", "", "telegram", "123")
	if err == nil {
		t.Error("expected error for invalid cron expression")
	}
}

func TestSchedulerCronInvalidTimezone(t *testing.T) {
	s := NewScheduler(func(j Job) {})

	_, err := s.AddScheduled("bad-tz", "test", "* * * * *", "Fake/Zone", "telegram", "123")
	if err == nil {
		t.Error("expected error for invalid timezone")
	}
}
