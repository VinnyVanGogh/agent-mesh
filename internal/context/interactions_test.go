package context

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)



func TestValidateInteractionPayload(t *testing.T) {
	// 1. ask_user_questions
	validQuestions := AskUserQuestionsPayload{
		Questions: []QuestionItem{
			{
				ID:       "q1",
				Question: "Which database should we use?",
				Options:  []string{"PostgreSQL", "SQLite"},
			},
		},
	}
	qBytes, _ := json.Marshal(validQuestions)
	if err := ValidateInteractionPayload(KindAskUserQuestions, string(qBytes)); err != nil {
		t.Fatalf("expected valid ask_user_questions payload, got: %v", err)
	}

	// invalid: less than 2 options
	invalidQuestions := AskUserQuestionsPayload{
		Questions: []QuestionItem{
			{Question: "Only one option?", Options: []string{"Yes"}},
		},
	}
	iqBytes, _ := json.Marshal(invalidQuestions)
	if err := ValidateInteractionPayload(KindAskUserQuestions, string(iqBytes)); err == nil {
		t.Fatalf("expected error for question with < 2 options")
	}

	// 2. request_confirmation
	validConf := RequestConfirmationPayload{
		Prompt: "Approve migration plan?",
		Target: ConfirmationTarget{
			Type:       "issue_document",
			Key:        "plan",
			RevisionId: 1,
		},
	}
	cBytes, _ := json.Marshal(validConf)
	if err := ValidateInteractionPayload(KindRequestConfirmation, string(cBytes)); err != nil {
		t.Fatalf("expected valid request_confirmation payload, got: %v", err)
	}

	// invalid: empty prompt
	invalidConf := RequestConfirmationPayload{Prompt: ""}
	icBytes, _ := json.Marshal(invalidConf)
	if err := ValidateInteractionPayload(KindRequestConfirmation, string(icBytes)); err == nil {
		t.Fatalf("expected error for empty confirmation prompt")
	}

	// 3. suggest_tasks
	validTasks := SuggestTasksPayload{
		Tasks: []SuggestedTaskItem{
			{Title: "Refactor auth"},
		},
	}
	sBytes, _ := json.Marshal(validTasks)
	if err := ValidateInteractionPayload(KindSuggestTasks, string(sBytes)); err != nil {
		t.Fatalf("expected valid suggest_tasks payload, got: %v", err)
	}

	// invalid: empty title
	invalidTasks := SuggestTasksPayload{
		Tasks: []SuggestedTaskItem{
			{Title: ""},
		},
	}
	isBytes, _ := json.Marshal(invalidTasks)
	if err := ValidateInteractionPayload(KindSuggestTasks, string(isBytes)); err == nil {
		t.Fatalf("expected error for suggested task with empty title")
	}

	// invalid kind
	if err := ValidateInteractionPayload("unknown_kind", string(sBytes)); err == nil {
		t.Fatalf("expected error for unknown kind")
	}
}

func TestPlanRevisionLocking_RejectsStale(t *testing.T) {
	testDB := setupTestDB(t)

	task, err := CreateTask(testDB, "Test Task", "/tmp/repo", "main", "personal")
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Add Document version 1
	if err := AddTaskDocument(testDB, task.ID, "plan", "# Initial Plan v1"); err != nil {
		t.Fatalf("failed to add task document: %v", err)
	}

	// Request confirmation targeting revision 1 -> should succeed
	confPayload := RequestConfirmationPayload{
		Prompt: "Please approve plan v1",
		Target: ConfirmationTarget{
			Type:       "issue_document",
			Key:        "plan",
			RevisionId: 1,
		},
	}
	payloadBytes, _ := json.Marshal(confPayload)

	in1, err := CreateInteraction(testDB, &TaskInteraction{
		TaskID:          task.ID,
		InteractionKind: KindRequestConfirmation,
		Payload:         string(payloadBytes),
	})
	if err != nil {
		t.Fatalf("expected confirmation for revision 1 to succeed, got: %v", err)
	}
	if in1.Status != InteractionStatusPending {
		t.Fatalf("expected pending status, got %s", in1.Status)
	}
	expectedIdemp := "confirmation:" + task.ID + ":plan:1"
	if in1.IdempotencyKey != expectedIdemp {
		t.Fatalf("expected idempotency key %s, got %s", expectedIdemp, in1.IdempotencyKey)
	}

	// Now update the document to revision 2
	if err := AddTaskDocument(testDB, task.ID, "plan", "# Updated Plan v2 with changes"); err != nil {
		t.Fatalf("failed to update task document: %v", err)
	}

	// Request confirmation targeting stale revision 1 -> MUST BE REJECTED
	staleConfPayload := RequestConfirmationPayload{
		Prompt: "Please approve old plan v1",
		Target: ConfirmationTarget{
			Type:       "issue_document",
			Key:        "plan",
			RevisionId: 1,
		},
		IdempotencyKey: "confirmation:unique-key-stale",
	}
	staleBytes, _ := json.Marshal(staleConfPayload)

	_, err = CreateInteraction(testDB, &TaskInteraction{
		TaskID:          task.ID,
		InteractionKind: KindRequestConfirmation,
		Payload:         string(staleBytes),
	})
	if err == nil {
		t.Fatalf("expected CreateInteraction to reject stale document revision, but it succeeded")
	}
	if !errors.Is(err, ErrStaleDocumentRevision) {
		t.Fatalf("expected ErrStaleDocumentRevision, got: %v", err)
	}

	// Request confirmation targeting non-existent document -> MUST FAIL
	nonExistentPayload := RequestConfirmationPayload{
		Prompt: "Please approve non-existent doc",
		Target: ConfirmationTarget{
			Type:       "issue_document",
			Key:        "architecture",
			RevisionId: 1,
		},
	}
	neBytes, _ := json.Marshal(nonExistentPayload)

	_, err = CreateInteraction(testDB, &TaskInteraction{
		TaskID:          task.ID,
		InteractionKind: KindRequestConfirmation,
		Payload:         string(neBytes),
	})
	if err == nil || !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("expected ErrDocumentNotFound, got: %v", err)
	}

	// Request confirmation targeting latest revision 2 -> should succeed
	validConf2 := RequestConfirmationPayload{
		Prompt: "Please approve plan v2",
		Target: ConfirmationTarget{
			Type:       "issue_document",
			Key:        "plan",
			RevisionId: 2,
		},
	}
	v2Bytes, _ := json.Marshal(validConf2)

	in2, err := CreateInteraction(testDB, &TaskInteraction{
		TaskID:          task.ID,
		InteractionKind: KindRequestConfirmation,
		Payload:         string(v2Bytes),
	})
	if err != nil {
		t.Fatalf("expected confirmation for revision 2 to succeed, got: %v", err)
	}
	if in2.IdempotencyKey != "confirmation:"+task.ID+":plan:2" {
		t.Fatalf("unexpected idempotency key: %s", in2.IdempotencyKey)
	}
}

func TestDuplicateIdempotencyKeys_AreNoOps(t *testing.T) {
	testDB := setupTestDB(t)

	task, err := CreateTask(testDB, "Test Task", "/tmp/repo", "main", "personal")
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	payload := SuggestTasksPayload{
		Tasks: []SuggestedTaskItem{
			{Title: "Task 1"},
			{Title: "Task 2"},
		},
	}
	pBytes, _ := json.Marshal(payload)

	customKey := "custom:idemp:123"

	// First creation
	first, err := CreateInteraction(testDB, &TaskInteraction{
		TaskID:          task.ID,
		InteractionKind: KindSuggestTasks,
		Payload:         string(pBytes),
		IdempotencyKey:  customKey,
	})
	if err != nil {
		t.Fatalf("first creation failed: %v", err)
	}

	// Second creation with identical idempotency key -> MUST RETURN FIRST WITHOUT ERROR
	second, err := CreateInteraction(testDB, &TaskInteraction{
		TaskID:          task.ID,
		InteractionKind: KindSuggestTasks,
		Payload:         string(pBytes),
		IdempotencyKey:  customKey,
	})
	if err != nil {
		t.Fatalf("second creation failed with error: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("expected duplicate idempotency key to return interaction #%d, got #%d", first.ID, second.ID)
	}

	// Ensure database only contains 1 interaction row
	interactions, err := ListInteractions(testDB, task.ID)
	if err != nil {
		t.Fatalf("failed to list interactions: %v", err)
	}
	if len(interactions) != 1 {
		t.Fatalf("expected exactly 1 interaction in db, found %d", len(interactions))
	}
}

func TestSupersedeOnComment(t *testing.T) {
	testDB := setupTestDB(t)

	task, err := CreateTask(testDB, "Test Task", "/tmp/repo", "main", "personal")
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// 1. Interaction with supersedeOnComment = true (default)
	payloadDefault := AskUserQuestionsPayload{
		Questions: []QuestionItem{
			{Question: "Q1?", Options: []string{"A", "B"}},
		},
	}
	pdBytes, _ := json.Marshal(payloadDefault)

	in1, err := CreateInteraction(testDB, &TaskInteraction{
		TaskID:          task.ID,
		InteractionKind: KindAskUserQuestions,
		Payload:         string(pdBytes),
	})
	if err != nil {
		t.Fatalf("failed to create interaction 1: %v", err)
	}
	if !in1.SupersedeOnComment {
		t.Fatalf("expected supersede_on_comment to default to true")
	}

	// 2. Interaction with supersedeOnUserComment = false explicitly
	noSupersede := false
	payloadPersistent := SuggestTasksPayload{
		Tasks:                  []SuggestedTaskItem{{Title: "Keep me open"}},
		SupersedeOnUserComment: &noSupersede,
	}
	ppBytes, _ := json.Marshal(payloadPersistent)

	in2, err := CreateInteraction(testDB, &TaskInteraction{
		TaskID:          task.ID,
		InteractionKind: KindSuggestTasks,
		Payload:         string(ppBytes),
	})
	if err != nil {
		t.Fatalf("failed to create interaction 2: %v", err)
	}
	if in2.SupersedeOnComment {
		t.Fatalf("expected supersede_on_comment to be false")
	}

	// Add a user comment to the task
	if err := AddTaskComment(testDB, task.ID, "reviewer", "Let's change our strategy instead."); err != nil {
		t.Fatalf("failed to add task comment: %v", err)
	}

	// Fetch updated interactions
	updated1, err := GetInteraction(testDB, in1.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated interaction 1: %v", err)
	}
	if updated1.Status != InteractionStatusSuperseded {
		t.Fatalf("expected interaction 1 to be superseded, got %s", updated1.Status)
	}
	if updated1.ResolvedAt == nil {
		t.Fatalf("expected resolved_at timestamp on superseded interaction")
	}

	updated2, err := GetInteraction(testDB, in2.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated interaction 2: %v", err)
	}
	if updated2.Status != InteractionStatusPending {
		t.Fatalf("expected interaction 2 to remain pending, got %s", updated2.Status)
	}
}

func TestResolveInteraction(t *testing.T) {
	testDB := setupTestDB(t)

	task, err := CreateTask(testDB, "Test Task", "/tmp/repo", "main", "personal")
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	payload := SuggestTasksPayload{
		Tasks: []SuggestedTaskItem{
			{ID: "t1", Title: "Task 1"},
			{ID: "t2", Title: "Task 2"},
		},
	}
	pBytes, _ := json.Marshal(payload)

	in, err := CreateInteraction(testDB, &TaskInteraction{
		TaskID:          task.ID,
		InteractionKind: KindSuggestTasks,
		Payload:         string(pBytes),
	})
	if err != nil {
		t.Fatalf("failed to create interaction: %v", err)
	}

	// Resolve as accepted with selected tasks
	resp := SuggestTasksResponse{
		AcceptedTasks: []SuggestedTaskItem{
			{ID: "t1", Title: "Task 1"},
		},
	}

	resolved, err := ResolveInteraction(testDB, in.ID, InteractionStatusAccepted, resp)
	if err != nil {
		t.Fatalf("failed to resolve interaction: %v", err)
	}

	if resolved.Status != InteractionStatusAccepted {
		t.Fatalf("expected accepted status, got %s", resolved.Status)
	}
	if resolved.ResolvedAt == nil {
		t.Fatalf("expected resolved_at timestamp")
	}
	if resolved.Response == "" {
		t.Fatalf("expected non-empty response JSON")
	}

	var parsedResp SuggestTasksResponse
	if err := json.Unmarshal([]byte(resolved.Response), &parsedResp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if len(parsedResp.AcceptedTasks) != 1 || parsedResp.AcceptedTasks[0].ID != "t1" {
		t.Fatalf("unexpected response contents: %+v", parsedResp)
	}

	// 1. Attempting to resolve an already resolved interaction must fail
	_, err = ResolveInteraction(testDB, in.ID, InteractionStatusRejected, nil)
	if err == nil {
		t.Fatalf("expected error resolving already resolved interaction")
	}
	expectedErr := fmt.Sprintf("interaction #%d already resolved (status: accepted)", in.ID)
	if err.Error() != expectedErr {
		t.Fatalf("expected error %q, got %q", expectedErr, err.Error())
	}

	// 2. Creating another pending interaction to test invalid status validation
	in2, err := CreateInteraction(testDB, &TaskInteraction{
		TaskID:          task.ID,
		InteractionKind: KindSuggestTasks,
		Payload:         string(pBytes),
	})
	if err != nil {
		t.Fatalf("failed to create interaction: %v", err)
	}

	// Attempting to resolve with invalid status string
	_, err = ResolveInteraction(testDB, in2.ID, "unknown_status", nil)
	if err == nil {
		t.Fatalf("expected error resolving with invalid status")
	}
	if !strings.Contains(err.Error(), "invalid terminal status") {
		t.Fatalf("expected 'invalid terminal status' error, got %v", err)
	}

	// Attempting to resolve with "pending" (not a terminal status)
	_, err = ResolveInteraction(testDB, in2.ID, InteractionStatusPending, nil)
	if err == nil {
		t.Fatalf("expected error resolving with pending status")
	}
	if !strings.Contains(err.Error(), "invalid terminal status") {
		t.Fatalf("expected 'invalid terminal status' error, got %v", err)
	}
}
