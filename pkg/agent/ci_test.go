package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestParseCIStructuredFields covers extraction of the structured CI
// investigation fields (ERROR_SUMMARY, ROOT_CAUSE, EVIDENCE, RECOMMENDATION,
// FAILING_TEST) from an agent result: complete and partial field sets, fully
// unstructured input, multi-line evidence that stops only at column-0 field
// headers, and skipping of the CLASSIFICATION line.
func TestParseCIStructuredFields(t *testing.T) {
	tests := []struct {
		name  string
		input string
		// wantExact maps field name to its exact expected value; an empty
		// string asserts the field must be empty.
		wantExact map[string]string
		// wantContains and wantNotContains map field name to substrings the
		// field must or must not contain.
		wantContains    map[string][]string
		wantNotContains map[string][]string
	}{
		{
			name: "all fields",
			input: `ERROR_SUMMARY: test assertion failed in kubevirt handler

ROOT_CAUSE: The PR changed GenerateNetworkOverrideMachineConfig() but didn't update the test fixture to include the new nmstate config fields.

EVIDENCE:
TestKubeVirtHandler/should_generate_nmstate_config:
  Expected: "autoconf: false"
  Got: ""

RECOMMENDATION: Update test fixtures to include new nmstate config fields.

FAILING_TEST: TestKubeVirtHandler/should_generate_nmstate_config`,
			wantExact: map[string]string{
				"errorSummary": "test assertion failed in kubevirt handler",
				"failingTest":  "TestKubeVirtHandler/should_generate_nmstate_config",
			},
			wantContains: map[string][]string{
				"rootCause":      {"GenerateNetworkOverrideMachineConfig"},
				"evidence":       {"autoconf: false"},
				"recommendation": {"Update test fixtures"},
			},
		},
		{
			name: "missing fields stay empty",
			input: `Some preamble text.

ERROR_SUMMARY: HTTP 500 from git server

ROOT_CAUSE: GitHub's git server returned HTTP 500 during clone.`,
			wantExact: map[string]string{
				"errorSummary":   "HTTP 500 from git server",
				"evidence":       "",
				"recommendation": "",
				"failingTest":    "",
			},
			wantContains: map[string][]string{
				"rootCause": {"HTTP 500"},
			},
		},
		{
			name:  "unstructured input yields no fields",
			input: "GitHub git server returned HTTP 500. This is an infrastructure failure.",
			wantExact: map[string]string{
				"errorSummary":   "",
				"rootCause":      "",
				"evidence":       "",
				"recommendation": "",
				"failingTest":    "",
			},
		},
		{
			name: "multi-line evidence stops at next field",
			input: `ERROR_SUMMARY: test timeout

EVIDENCE:
=== RUN TestNetworkPolicy
    timeout after 300s
    waiting for pod readiness
--- FAIL: TestNetworkPolicy (300.12s)

RECOMMENDATION: Retest with /retest`,
			wantExact: map[string]string{
				"recommendation": "Retest with /retest",
			},
			wantContains: map[string][]string{
				"evidence": {"TestNetworkPolicy", "timeout after 300s"},
			},
			wantNotContains: map[string][]string{
				"evidence": {"RECOMMENDATION"},
			},
		},
		{
			name: "classification line skipped",
			input: `CLASSIFICATION: INFRASTRUCTURE

ERROR_SUMMARY: DNS resolution failure

ROOT_CAUSE: Cluster DNS was unavailable.`,
			wantExact: map[string]string{
				"errorSummary": "DNS resolution failure",
				"rootCause":    "Cluster DNS was unavailable.",
			},
		},
		{
			// Indented lines that look like field headers must not terminate
			// evidence capture because they are not at column 0.
			name: "indented field-like lines stay in evidence",
			input: `ERROR_SUMMARY: test failure

EVIDENCE:
  test output:
    RECOMMENDATION: some test recommendation output
    ROOT_CAUSE: some diagnostic line
  more test output

RECOMMENDATION: Retest with /retest`,
			wantExact: map[string]string{
				"recommendation": "Retest with /retest",
			},
			wantContains: map[string][]string{
				"evidence": {
					"RECOMMENDATION: some test recommendation output",
					"ROOT_CAUSE: some diagnostic line",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errorSummary, rootCause, evidence, recommendation, failingTest := parseCIStructuredFields(tt.input)
			got := map[string]string{
				"errorSummary":   errorSummary,
				"rootCause":      rootCause,
				"evidence":       evidence,
				"recommendation": recommendation,
				"failingTest":    failingTest,
			}
			for field, want := range tt.wantExact {
				if got[field] != want {
					t.Errorf("%s = %q, want %q", field, got[field], want)
				}
			}
			for field, subs := range tt.wantContains {
				for _, sub := range subs {
					if !strings.Contains(got[field], sub) {
						t.Errorf("%s missing %q, got %q", field, sub, got[field])
					}
				}
			}
			for field, subs := range tt.wantNotContains {
				for _, sub := range subs {
					if strings.Contains(got[field], sub) {
						t.Errorf("%s should not contain %q, got %q", field, sub, got[field])
					}
				}
			}
		})
	}
}

func TestFormatCISummaryHeader(t *testing.T) {
	infra := []ciResult{
		{check: "test-deploy", category: "infrastructure"},
		{check: "check-license", category: "infrastructure"},
	}
	unrelated := []ciResult{
		{check: "e2e-bgp", category: "unrelated"},
	}
	related := []ciResult{
		{check: "unit-tests", category: "related", pushed: true},
	}

	header := formatCISummaryHeader("abc1234567890", infra, unrelated, related)

	if !strings.Contains(header, "CI Failure Analysis") {
		t.Errorf("header missing title")
	}
	if !strings.Contains(header, "abc1234") {
		t.Errorf("header missing short SHA")
	}
	if !strings.Contains(header, "**Total failures**: 4") {
		t.Errorf("header has wrong total, got: %s", header)
	}
	if !strings.Contains(header, "2 infrastructure") {
		t.Errorf("header missing infrastructure count")
	}
	if !strings.Contains(header, "1 unrelated") {
		t.Errorf("header missing unrelated count")
	}
	if !strings.Contains(header, "1 related") {
		t.Errorf("header missing related count")
	}
	if !strings.Contains(header, "Pushed fix") {
		t.Errorf("header missing pushed fix action")
	}
}

// TestFormatCIRelatedDetails covers the collapsible details block for a
// related CI failure: the full section layout when a fix was pushed, the
// fix-needed variant without an Action section, and HTML escaping of the
// check name.
func TestFormatCIRelatedDetails(t *testing.T) {
	tests := []struct {
		name            string
		r               ciResult
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "pushed fix renders all sections",
			r: ciResult{
				check:          "unit-tests",
				category:       "related",
				errorSummary:   "test assertion failed",
				rootCause:      "PR changed the handler but test fixture was not updated.",
				evidence:       "Expected: \"autoconf: false\"\nGot: \"\"",
				recommendation: "Update test fixtures.",
				pushed:         true,
			},
			wantContains: []string{
				"<details>",
				"🔴 Related",
				"<code>unit-tests</code>",
				"fix pushed",
				"### Error",
				"autoconf: false",
				"### Root Cause",
				"handler but test fixture",
				"### Action",
				"</details>",
			},
		},
		{
			name: "not pushed omits Action section",
			r: ciResult{
				check:          "lint",
				category:       "related",
				errorSummary:   "lint failure",
				recommendation: "Fix lint errors.",
			},
			wantContains:    []string{"fix needed"},
			wantNotContains: []string{"### Action"},
		},
		{
			// Check name must be HTML-escaped inside <code> tags.
			name: "HTML-escapes check name",
			r: ciResult{
				check:        "pull-ci-<org>/repo-e2e",
				category:     "related",
				errorSummary: "test failed",
			},
			wantContains:    []string{"&lt;org&gt;"},
			wantNotContains: []string{"<org>"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := formatCIRelatedDetails(tt.r)
			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got: %s", want, output)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(output, notWant) {
					t.Errorf("output should not contain %q, got: %s", notWant, output)
				}
			}
		})
	}
}

// TestFormatCIUnrelatedDetails covers the collapsible details block for an
// unrelated CI failure: the flaky-issue reference and Known Issue section
// when a flaky issue exists, their absence when none does, and HTML escaping
// of the check name.
func TestFormatCIUnrelatedDetails(t *testing.T) {
	tests := []struct {
		name            string
		r               ciResult
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "flaky issue referenced",
			r: ciResult{
				check:          "e2e-conformance",
				category:       "unrelated",
				errorSummary:   "SCTP ingress test timeout",
				rootCause:      "This test has been flaking independently due to cluster network latency.",
				evidence:       "TestNetworkPolicyV2 — timeout after 300s",
				recommendation: "Skip or quarantine the test.",
				failingTest:    "TestNetworkPolicyV2",
				flakyIssue:     6381,
			},
			wantContains: []string{
				"<details>",
				"⚠️ Unrelated",
				"flaky test (#6381)",
				"### Known Issue",
				"#6381",
			},
		},
		{
			name: "no flaky issue omits Known Issue section",
			r: ciResult{
				check:        "e2e-network",
				category:     "unrelated",
				errorSummary: "network timeout",
			},
			wantContains:    []string{"flaky test"},
			wantNotContains: []string{"Known Issue"},
		},
		{
			name: "HTML-escapes check name",
			r: ciResult{
				check:        "e2e-<shard>&test",
				category:     "unrelated",
				errorSummary: "flaky timeout",
			},
			wantContains: []string{"&lt;shard&gt;&amp;test"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &Agent{cfg: Config{}}
			output := formatCIUnrelatedDetails(tt.r, agent)
			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got: %s", want, output)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(output, notWant) {
					t.Errorf("output should not contain %q, got: %s", notWant, output)
				}
			}
		})
	}
}

// TestFormatCIInfrastructureSection covers grouping of infrastructure
// failures in the consolidated comment: identical errors collapse into one
// grouped block listing every check, a single check gets its own compact
// block, mixed errors split into separate groups, and single-check names are
// HTML-escaped.
func TestFormatCIInfrastructureSection(t *testing.T) {
	tests := []struct {
		name         string
		infra        []ciResult
		wantContains []string
	}{
		{
			name: "grouped identical errors",
			infra: []ciResult{
				{check: "test-deploy", category: "infrastructure", errorSummary: "HTTP 500 from git server", rootCause: "GitHub outage", recommendation: "Retest with /retest"},
				{check: "check-license-header", category: "infrastructure", errorSummary: "HTTP 500 from git server"},
				{check: "e2e-dual-conversion", category: "infrastructure", errorSummary: "HTTP 500 from git server"},
			},
			// All checks appear in the table; root cause and recommendation
			// come from the first result.
			wantContains: []string{
				"🔧 Infrastructure (3)",
				"HTTP 500 from git server",
				"`test-deploy`",
				"`check-license-header`",
				"`e2e-dual-conversion`",
				"GitHub outage",
				"Retest with /retest",
			},
		},
		{
			name: "single check",
			infra: []ciResult{
				{check: "build", category: "infrastructure", errorSummary: "OOM killed", rootCause: "Runner ran out of memory."},
			},
			wantContains: []string{
				"🔧 Infrastructure: <code>build</code>",
				"OOM killed",
				"Runner ran out of memory",
			},
		},
		{
			// Two groups: one with 2 checks (HTTP 500) and one single (OOM).
			name: "mixed errors split into groups",
			infra: []ciResult{
				{check: "test-deploy", category: "infrastructure", errorSummary: "HTTP 500 from git server"},
				{check: "e2e-bgp", category: "infrastructure", errorSummary: "HTTP 500 from git server"},
				{check: "build", category: "infrastructure", errorSummary: "OOM killed"},
			},
			wantContains: []string{
				"Infrastructure (2)",
				"Infrastructure: <code>build</code>",
			},
		},
		{
			name: "HTML-escapes single check name",
			infra: []ciResult{
				{check: "build-<arch>", category: "infrastructure", errorSummary: "OOM killed"},
			},
			wantContains: []string{"&lt;arch&gt;"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := formatCIInfrastructureSection(tt.infra)
			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got: %s", want, output)
				}
			}
		})
	}
}

// TestResultSummaryLine covers the one-line summary choice: a structured
// error summary wins over the free-form explanation, which is truncated to
// its first sentence when used as the fallback.
func TestResultSummaryLine(t *testing.T) {
	tests := []struct {
		name string
		r    ciResult
		want string
	}{
		{
			name: "prefers error summary",
			r: ciResult{
				errorSummary: "structured summary",
				explanation:  "Full explanation with many details. Second sentence.",
			},
			want: "structured summary",
		},
		{
			name: "falls back to first sentence of explanation",
			r:    ciResult{explanation: "Full explanation with many details. Second sentence."},
			want: "Full explanation with many details.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resultSummaryLine(tt.r); got != tt.want {
				t.Errorf("resultSummaryLine = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEscapeHTML(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"no escaping needed", "no escaping needed"},
		{"<script>alert('xss')</script>", "&lt;script&gt;alert('xss')&lt;/script&gt;"},
		{"a & b", "a &amp; b"},
		{"a < b > c", "a &lt; b &gt; c"},
	}
	for _, tt := range tests {
		got := escapeHTML(tt.input)
		if got != tt.want {
			t.Errorf("escapeHTML(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestWriteStructuredBody covers section emission in writeStructuredBody:
// every section when evidence, root cause, and recommendation are set, the
// summary as Error-section fallback when evidence is missing, and no
// sections at all for an empty result.
func TestWriteStructuredBody(t *testing.T) {
	tests := []struct {
		name            string
		summary         string
		r               ciResult
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:    "all fields",
			summary: "summary line",
			r: ciResult{
				evidence:       "line 1\nline 2",
				rootCause:      "Something broke.",
				recommendation: "Fix it.",
			},
			wantContains: []string{
				"### Error",
				"line 1\nline 2",
				"### Root Cause",
				"### Recommendation",
			},
		},
		{
			name:         "no evidence uses summary",
			summary:      "fallback summary",
			wantContains: []string{"fallback summary"},
		},
		{
			name:            "empty emits no sections",
			wantNotContains: []string{"### Error", "### Root Cause"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			writeStructuredBody(&b, tt.summary, tt.r)
			output := b.String()
			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got: %s", want, output)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(output, notWant) {
					t.Errorf("output should not contain %q, got: %s", notWant, output)
				}
			}
		})
	}
}

// TestWriteFenced covers fence selection in writeFenced: a plain body gets a
// standard triple-backtick fence, and a body containing backtick runs gets a
// fence longer than its longest run so the content cannot break out.
func TestWriteFenced(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantContains []string
	}{
		{
			name:         "no backticks uses standard fence",
			body:         "plain error text",
			wantContains: []string{"```\nplain error text\n```"},
		},
		{
			// A 4-backtick fence avoids breakout and the body is preserved unchanged.
			name:         "triple backticks in body use longer fence",
			body:         "Error in ```yaml\nkey: value\n```",
			wantContains: []string{"````", "Error in ```yaml\nkey: value\n```"},
		},
		{
			// Fence must be longer than 5, the longest backtick run in the body.
			name:         "long backtick run uses even longer fence",
			body:         "Some `````long````` backtick run",
			wantContains: []string{"``````"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			writeFenced(&b, "### Error", tt.body)
			output := b.String()
			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got: %s", want, output)
				}
			}
		})
	}
}

func TestProcessCIFailures_FixesFailingCI(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed CI"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "tests failed"},
		},
		prHeadSHAs: []string{"sha-before", "sha-after"}, // different SHAs = Claude pushed
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	claudeCalls := countCalls(runner.calls, "claude")
	if claudeCalls != 1 {
		t.Fatalf("expected 1 claude call, got %d", claudeCalls)
	}
	if agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].CIFixAttempts != 1 {
		t.Errorf("expected 1 CI fix attempt, got %d", agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].CIFixAttempts)
	}
}

// TestProcessCIFailures_SkipsWithoutInvokingClaude covers the reasons a CI
// poll ends without invoking claude or posting comments: CI passed, the
// fix-attempt budget is exhausted, or a check is still running.
func TestProcessCIFailures_SkipsWithoutInvokingClaude(t *testing.T) {
	tests := []struct {
		name          string
		checkRun      CheckRun
		ciFixAttempts int
	}{
		{
			name:     "passing CI",
			checkRun: CheckRun{ID: 1, Name: "test", Status: "completed", Conclusion: "success"},
		},
		{
			name:          "max retries reached",
			checkRun:      CheckRun{ID: 1, Name: "test", Status: "completed", Conclusion: "failure"},
			ciFixAttempts: 3,
		},
		{
			name:     "pending CI",
			checkRun: CheckRun{ID: 1, Name: "test", Status: "in_progress", Conclusion: ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := &mockGitHubClient{checkRuns: []CheckRun{tt.checkRun}}
			runner := &mockCommandRunner{}
			wt := &mockWorktreeManager{}

			agent := newTestAgent(gh, runner, wt)
			trackWork(agent, func(w *IssueWork) { w.CIFixAttempts = tt.ciFixAttempts })

			agent.ProcessCIFailures(context.Background())

			if len(runner.calls) != 0 {
				t.Error("should not invoke claude")
			}
			if len(gh.addedComments) != 0 {
				t.Errorf("expected no comments, got %v", gh.addedComments)
			}
		})
	}
}

func TestProcessCIFailures_NoRunsDoesNotMarkChecked(t *testing.T) {
	// Issue #139: When no check runs are registered yet (e.g., oompa polls
	// before GitHub registers CI), allCompleted is vacuously true. The agent
	// must NOT set LastCheckedCISHA in this case, otherwise real CI failures
	// that appear later will be skipped by the fast path.
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{}, // No check runs registered yet
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	if len(runner.calls) != 0 {
		t.Error("should not invoke claude when no check runs exist")
	}

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCheckedCISHA != "" {
		t.Errorf("expected LastCheckedCISHA to remain empty when no check runs registered, got %q", work.LastCheckedCISHA)
	}
}

func TestProcessCIFailures_NoRunsThenFailuresAreInvestigated(t *testing.T) {
	// Issue #139 end-to-end regression: when no check runs exist on poll 1,
	// LastCheckedCISHA must stay empty so that poll 2 (when runs appear with
	// a failure) actually invokes Claude instead of silently skipping.
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed CI"})
	gh := &mockGitHubClient{
		checkRuns:  []CheckRun{}, // empty on first poll
		prHeadSHAs: []string{"sha1", "sha1", "sha1"},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	// Poll 1: no runs yet — must not mark SHA as checked
	agent.ProcessCIFailures(context.Background())
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCheckedCISHA != "" {
		t.Fatalf("poll 1: expected LastCheckedCISHA empty, got %q", work.LastCheckedCISHA)
	}
	if countCalls(runner.calls, "claude") != 0 {
		t.Fatalf("poll 1: expected 0 claude calls, got %d", countCalls(runner.calls, "claude"))
	}

	// Poll 2: CI runs now registered with a failure
	gh.checkRuns = []CheckRun{
		{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "tests failed"},
	}
	agent.ProcessCIFailures(context.Background())
	if countCalls(runner.calls, "claude") != 1 {
		t.Errorf("poll 2: expected 1 claude call, got %d", countCalls(runner.calls, "claude"))
	}
}

func TestProcessCIFailures_CreatesFlakyIssueWhenUnrelated(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED\nFAILING_TEST: TestDB/connection_timeout\nThe test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true // Enable flaky issue creation
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Check that a flaky issue was created with the failing test name in the title
	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created issue, got %d", len(gh.createdIssues))
	}
	issue := gh.createdIssues[0]
	if issue.Title != "Flaky CI: integration-tests / TestDB/connection_timeout" {
		t.Errorf("expected title 'Flaky CI: integration-tests / TestDB/connection_timeout', got %q", issue.Title)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "flaky-test" {
		t.Errorf("expected labels ['flaky-test'], got %v", issue.Labels)
	}
	// Check the body uses the flaking-test issue template format
	if !strings.Contains(issue.Body, "### Which jobs are flaking?") {
		t.Errorf("expected issue body to contain '### Which jobs are flaking?', got %q", issue.Body)
	}
	if !strings.Contains(issue.Body, "### Which tests are flaking?") {
		t.Errorf("expected issue body to contain '### Which tests are flaking?', got %q", issue.Body)
	}
	if !strings.Contains(issue.Body, "### Since when has it been flaking?") {
		t.Errorf("expected issue body to contain '### Since when has it been flaking?', got %q", issue.Body)
	}
	if !strings.Contains(issue.Body, "### Reason for failure (if possible)") {
		t.Errorf("expected issue body to contain '### Reason for failure (if possible)', got %q", issue.Body)
	}
	if !strings.Contains(issue.Body, "Automatically created by [oompa]") {
		t.Errorf("expected issue body to contain oompa attribution, got %q", issue.Body)
	}
	// Body should use the failing test name, not the lane name, in the "Which tests" section
	if !strings.Contains(issue.Body, "TestDB/connection_timeout") {
		t.Errorf("expected issue body to contain the failing test name, got %q", issue.Body)
	}
	// Body should NOT contain the raw FAILING_TEST: line
	if strings.Contains(issue.Body, "FAILING_TEST:") {
		t.Errorf("expected FAILING_TEST: line to be stripped from issue body, got %q", issue.Body)
	}

	// Check that a single consolidated comment was added to the PR
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}
	// Consolidated comment should mention the flaky issue
	if !strings.Contains(gh.addedComments[0], "#1") {
		t.Errorf("expected consolidated comment to reference flaky issue #1, got: %q", gh.addedComments[0])
	}
}

func TestProcessCIFailures_InfrastructureSkipsFlakyIssue(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "INFRASTRUCTURE Fedora koji server returned HTTP 502 Bad Gateway"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "Build-PR", Status: "completed", Conclusion: "failure", Output: "HTTP 502 Bad Gateway from koji.fedoraproject.org"},
		},
		checkRunLogs: map[int64]string{
			1: "Building package...\nFetching from koji.fedoraproject.org...\nHTTP 502 Bad Gateway\nBuild failed",
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true // Even with flaky issues enabled, INFRASTRUCTURE should skip
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Check that NO flaky issue was created (infrastructure != flaky)
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues for INFRASTRUCTURE classification, got %d", len(gh.createdIssues))
	}

	// Check that exactly 1 consolidated comment was posted
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}

	// Verify the comment mentions infrastructure
	if !strings.Contains(gh.addedComments[0], "Infrastructure:") {
		t.Errorf("expected comment to mention Infrastructure, got: %q", gh.addedComments[0])
	}
	if !strings.Contains(gh.addedComments[0], "Build-PR") {
		t.Errorf("expected comment to mention the check name, got: %q", gh.addedComments[0])
	}

	// Verify state was updated correctly
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCIStatus != "infrastructure-failure" {
		t.Errorf("expected LastCIStatus 'infrastructure-failure', got %q", work.LastCIStatus)
	}
	if work.CIFixAttempts != 0 {
		t.Errorf("expected 0 CI fix attempts for infrastructure failure, got %d", work.CIFixAttempts)
	}
}

func TestProcessCIFailures_SkipsFlakyIssueWhenDisabled(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false // Disabled by default
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Check that no flaky issue was created
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues when feature is disabled, got %d", len(gh.createdIssues))
	}

	// Check that only one comment was added (the unrelated notice)
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment (unrelated notice), got %d", len(gh.addedComments))
	}
}

func TestProcessCIFailures_SkipCommentCIUnrelated(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipComments = []string{"ci-unrelated"}
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// No comments should be posted (comment skipped, dedup via state)
	if len(gh.addedComments) != 0 {
		t.Fatalf("expected 0 comments (ci-unrelated skipped), got %d: %v", len(gh.addedComments), gh.addedComments)
	}

	// State should still be updated
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCIStatus != "unrelated-failure" {
		t.Errorf("expected LastCIStatus 'unrelated-failure', got %q", work.LastCIStatus)
	}
	// Check should be recorded in state for dedup
	if !work.CheckedCIChecks["abc123:integration-tests"] {
		t.Error("expected check to be recorded in CheckedCIChecks for dedup")
	}
}

func TestProcessCIFailures_SkipCommentCIUnrelated_StillCreatesFlakyIssue(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipComments = []string{"ci-unrelated"}
	agent.cfg.CreateFlakyIssues = true
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Flaky issue should still be created even though ci-unrelated comment is skipped
	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created flaky issue, got %d", len(gh.createdIssues))
	}

	// No comments should be posted — the unrelated section is skipped (ci-unrelated),
	// so the consolidated comment has no visible content and is suppressed.
	if len(gh.addedComments) != 0 {
		t.Fatalf("expected 0 comments (ci-unrelated skipped, no visible sections), got %d: %v", len(gh.addedComments), gh.addedComments)
	}
}

func TestProcessCIFailures_SkipCommentCIInfrastructure(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "INFRASTRUCTURE Fedora koji server returned HTTP 502 Bad Gateway"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "Build-PR", Status: "completed", Conclusion: "failure", Output: "HTTP 502 Bad Gateway from koji.fedoraproject.org"},
		},
		checkRunLogs: map[int64]string{
			1: "Building package...\nFetching from koji.fedoraproject.org...\nHTTP 502 Bad Gateway\nBuild failed",
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipComments = []string{"ci-infrastructure"}
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// No comments should be posted
	if len(gh.addedComments) != 0 {
		t.Fatalf("expected 0 comments (ci-infrastructure skipped), got %d: %v", len(gh.addedComments), gh.addedComments)
	}

	// State should still be updated
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCIStatus != "infrastructure-failure" {
		t.Errorf("expected LastCIStatus 'infrastructure-failure', got %q", work.LastCIStatus)
	}
	// Check should be recorded in state for dedup
	if !work.CheckedCIChecks["abc123:Build-PR"] {
		t.Error("expected check to be recorded in CheckedCIChecks for dedup")
	}
}

// TestProcessCIFailures_CheckedCIChecksPopulatedWhenCommentsPosted verifies that
// the in-memory CheckedCIChecks map is populated even when comments are posted
// (the default, non-skip path). This is the primary dedup mechanism; comment
// markers are a secondary fallback that can be lost if comments are deleted.
func TestProcessCIFailures_CheckedCIChecksPopulatedWhenCommentsPosted(t *testing.T) {
	// Test all three classification categories
	tests := []struct {
		name           string
		claudeResponse string
		checkName      string
		wantCIStatus   string
	}{
		{
			name:           "infrastructure",
			claudeResponse: "INFRASTRUCTURE Fedora koji server returned HTTP 502 Bad Gateway",
			checkName:      "Build-PR",
			wantCIStatus:   "infrastructure-failure",
		},
		{
			name:           "unrelated",
			claudeResponse: "UNRELATED The test database connection times out intermittently",
			checkName:      "integration-tests",
			wantCIStatus:   "unrelated-failure",
		},
		{
			name:           "related-skip-fix",
			claudeResponse: "RELATED The unit test fails because the new function returns nil",
			checkName:      "unit-tests",
			wantCIStatus:   "related-skip-fix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claudeResult := streamResultJSON(AgentResult{Result: tt.claudeResponse})
			gh := &mockGitHubClient{
				checkRuns: []CheckRun{
					{ID: 1, Name: tt.checkName, Status: "completed", Conclusion: "failure", Output: "Error details here for analysis context padding to reach the 50 char minimum threshold"},
				},
				checkRunLogs: map[int64]string{
					1: "Build log output with enough content for analysis context padding to reach minimum",
				},
			}
			runner := &mockCommandRunner{stdout: claudeResult}
			wt := &mockWorktreeManager{}

			agent := newTestAgent(gh, runner, wt)
			agent.cfg.SkipFix = true // Prevent push attempts for RELATED
			// SkipComments is empty — comments will be posted (default behavior)
			trackWork(agent)

			agent.ProcessCIFailures(context.Background())

			work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]

			// Verify in-memory dedup map is populated even though comments are posted
			dedupKey := "abc123:" + tt.checkName
			if !work.CheckedCIChecks[dedupKey] {
				t.Errorf("expected CheckedCIChecks[%q] to be true when comments are posted (not skipped), but it was false", dedupKey)
			}

			// Verify status was set correctly
			if work.LastCIStatus != tt.wantCIStatus {
				t.Errorf("expected LastCIStatus %q, got %q", tt.wantCIStatus, work.LastCIStatus)
			}

			// Verify a comment was actually posted (confirming we're testing the non-skip path)
			if len(gh.addedComments) == 0 {
				t.Error("expected at least 1 comment to be posted (testing non-skip path), got 0")
			}
		})
	}
}

func TestProcessCIFailures_SkipCommentFlaky(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipComments = []string{"flaky"}
	agent.cfg.CreateFlakyIssues = true
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Flaky issue should still be created
	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created flaky issue, got %d", len(gh.createdIssues))
	}

	// Should have only the consolidated comment (flaky issue column suppressed by skip-comment: flaky)
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment (flaky ref suppressed), got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "Unrelated:") {
		t.Errorf("expected unrelated section in consolidated comment, got: %q", gh.addedComments[0])
	}
	// With flaky comment skipped, the issue reference should NOT appear in the Known Issue section
	if strings.Contains(gh.addedComments[0], "Known Issue") {
		t.Errorf("expected Known Issue section to be suppressed when skip-comment: flaky, got: %q", gh.addedComments[0])
	}
}

func TestProcessCIFailures_SkipsDuplicateFlakyIssue(t *testing.T) {
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	matchResult := streamResultJSON(AgentResult{Result: "MATCH 50"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{
			// Title does NOT match exactly — use a different title to exercise LLM path
			{Number: 50, Title: "Flaky CI: db-integration", Labels: []string{"flaky-test"}},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{ciResult, matchResult}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Check that no new issue was created (existing one should be referenced)
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues (should reference existing), got %d", len(gh.createdIssues))
	}

	// Check that comments were added:
	// 1. CI lane link on the flaky issue (#50) — per-check side effect
	// 2. consolidated comment on PR with flaky issue reference in table
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (CI lane link + consolidated), got %d", len(gh.addedComments))
	}

	// Verify the CI lane link comment (posted on the flaky issue #50)
	if !strings.Contains(gh.addedComments[0], "CI failure on PR #100") {
		t.Errorf("expected CI lane link comment, got: %q", gh.addedComments[0])
	}
	if gh.addedCommentTargets[0] != 50 {
		t.Errorf("expected CI lane link comment posted to issue #50, got #%d", gh.addedCommentTargets[0])
	}

	// Verify the consolidated comment on the PR references the flaky issue
	if !strings.Contains(gh.addedComments[1], "#50") {
		t.Errorf("expected consolidated comment to reference flaky issue #50, got: %q", gh.addedComments[1])
	}
	if gh.addedCommentTargets[1] != 100 {
		t.Errorf("expected consolidated comment posted to PR #100, got #%d", gh.addedCommentTargets[1])
	}
}

func TestProcessCIFailures_TitlePreCheckSkipsLLMMatching(t *testing.T) {
	// When an existing issue has an exact title match ("Flaky CI: <check-name>"),
	// the agent should skip LLM matching entirely and use the existing issue.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The Fedora koji server returned 502 Bad Gateway"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "Build-PR", Status: "completed", Conclusion: "failure", Output: "Error: 502 Bad Gateway from koji.fedoraproject.org"},
		},
		checkRunLogs: map[int64]string{
			1: "Building package...\nFetching from koji.fedoraproject.org...\nHTTP 502 Bad Gateway\nBuild failed",
		},
		searchResults: []Issue{
			{Number: 99, Title: "Flaky CI: Build-PR", Body: "koji infrastructure failure", Labels: []string{"flaky-test"}},
		},
	}
	// Only one Claude result needed (for CI investigation). No match result needed
	// because the title pre-check should prevent the LLM matching call.
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Should NOT have created a new issue
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues (title pre-check should match), got %d", len(gh.createdIssues))
	}

	// Only 1 claude call (CI investigation), NOT 2 (CI + matching)
	claudeCalls := countCalls(runner.calls, "claude")
	if claudeCalls != 1 {
		t.Errorf("expected 1 claude call (CI investigation only, no LLM matching), got %d", claudeCalls)
	}

	// Should have 2 comments:
	// 1. CI lane link on the flaky issue (#99) — per-check side effect
	// 2. consolidated comment on PR with flaky issue reference in table
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (CI lane link + consolidated), got %d", len(gh.addedComments))
	}

	// Verify the CI lane link comment (posted on the flaky issue)
	if !strings.Contains(gh.addedComments[0], "CI failure on PR #100") {
		t.Errorf("expected CI lane link comment, got: %q", gh.addedComments[0])
	}

	// Verify the consolidated comment references flaky issue #99
	if !strings.Contains(gh.addedComments[1], "#99") {
		t.Errorf("expected consolidated comment to reference flaky issue #99, got: %q", gh.addedComments[1])
	}
}

func TestProcessCIFailures_CreatesNewFlakyIssueWhenNoDuplicate(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{}, // No existing issues
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Check that a new issue was created
	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created issue, got %d", len(gh.createdIssues))
	}

	issue := gh.createdIssues[0]
	if issue.Title != "Flaky CI: integration-tests" {
		t.Errorf("expected title 'Flaky CI: integration-tests', got %q", issue.Title)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "flaky-test" {
		t.Errorf("expected labels ['flaky-test'], got %v", issue.Labels)
	}

	// Check that a single consolidated comment was added referencing the new flaky issue
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "#1") {
		t.Errorf("expected consolidated comment to reference flaky issue #1, got: %q", gh.addedComments[0])
	}
}

func TestProcessCIFailures_CreatesIssueWhenClaudeSaysNone(t *testing.T) {
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	matchResult := streamResultJSON(AgentResult{Result: "NONE"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{
			{Number: 50, Title: "Some other flaky test", Labels: []string{"flaky-test"}},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{ciResult, matchResult}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Claude said NONE, so a new issue should be created
	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created issue, got %d", len(gh.createdIssues))
	}
	if gh.createdIssues[0].Title != "Flaky CI: integration-tests" {
		t.Errorf("expected title 'Flaky CI: integration-tests', got %q", gh.createdIssues[0].Title)
	}
}

func TestProcessCIFailures_SearchAndLinkWithoutCreateFlakyIssues(t *testing.T) {
	// Issue #171: create-flaky-issues=false + flaky-label set + matching issue exists
	// → PR comment references the issue, CI lane link added to the issue, no new issue created.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{
			{Number: 1234, Title: "Flaky CI: integration-tests", Labels: []string{"kind/ci-flake"}},
		},
	}
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false    // Disabled — don't create new issues
	agent.cfg.FlakyLabel = "kind/ci-flake" // But label is set — enables search-and-link
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// No new issue should be created
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues (create-flaky-issues=false), got %d", len(gh.createdIssues))
	}

	// Should have 2 comments:
	// 1. CI lane link comment on the existing flaky issue (#1234) — per-check side effect
	// 2. consolidated comment on PR with flaky issue reference in table
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (CI lane link + consolidated), got %d", len(gh.addedComments))
	}

	// Verify CI lane link on the flaky issue
	if !strings.Contains(gh.addedComments[0], "CI failure on PR #100") {
		t.Errorf("expected CI lane link comment, got: %q", gh.addedComments[0])
	}
	if !strings.Contains(gh.addedComments[0], "integration-tests") {
		t.Errorf("expected CI lane link to mention CI lane name, got: %q", gh.addedComments[0])
	}

	// Verify the consolidated comment references flaky issue #1234
	if !strings.Contains(gh.addedComments[1], "#1234") {
		t.Errorf("expected consolidated comment to reference flaky issue #1234, got: %q", gh.addedComments[1])
	}
}

func TestProcessCIFailures_NoMatchNoCreateWhenDisabled(t *testing.T) {
	// Issue #171: create-flaky-issues=false + flaky-label set + no matching issue
	// → regular unrelated comment, no issue created.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{}, // No matching issues
	}
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false
	agent.cfg.FlakyLabel = "kind/ci-flake"
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// No issue should be created
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues (create-flaky-issues=false, no match), got %d", len(gh.createdIssues))
	}

	// Only the consolidated unrelated comment should be posted
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "Unrelated:") {
		t.Errorf("expected consolidated comment with unrelated section, got: %q", gh.addedComments[0])
	}
}

func TestProcessCIFailures_NoSearchWhenFlakyLabelEmpty(t *testing.T) {
	// Issue #171: flaky-label not set → no search, no linking.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
	}
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.FlakyLabel = "" // No flaky label
	agent.cfg.CreateFlakyIssues = false
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// No issue should be created
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues, got %d", len(gh.createdIssues))
	}

	// Only the unrelated comment
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment (unrelated notice only), got %d", len(gh.addedComments))
	}
}

func TestProcessCIFailures_CILaneLinkIncludesJobURL(t *testing.T) {
	// Issue #171: CI lane link comment includes the correct job URL and PR reference.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test timed out intermittently due to a flaky network connection in the CI environment"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 67890, Name: "e2e (control-plane, HA, shared, ipv4)", Status: "completed", Conclusion: "failure", Output: "Error: test timed out waiting for condition", HTMLURL: "https://github.com/owner/repo/actions/runs/12345/job/67890"},
		},
		checkRunLogs: map[int64]string{
			67890: "Running e2e tests...\nTimeout: waiting for pod to be ready\nTest failed after 300s\nStack trace:\n  at e2e.waitForPod(e2e.go:142)",
		},
		searchResults: []Issue{
			{Number: 5678, Title: "Flaky CI: e2e (control-plane, HA, shared, ipv4)", Labels: []string{"kind/ci-flake"}},
		},
	}
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false
	agent.cfg.FlakyLabel = "kind/ci-flake"
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Should have 2 comments:
	// 1. CI lane link on the flaky issue (#5678) — per-check side effect
	// 2. consolidated comment on PR with flaky issue reference in table
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (CI lane link + consolidated), got %d", len(gh.addedComments))
	}

	ciLaneComment := gh.addedComments[0]
	if !strings.Contains(ciLaneComment, "CI failure on PR #100") {
		t.Errorf("expected CI lane link to reference PR #100, got: %q", ciLaneComment)
	}
	if !strings.Contains(ciLaneComment, "e2e (control-plane, HA, shared, ipv4)") {
		t.Errorf("expected CI lane link to mention check name, got: %q", ciLaneComment)
	}
	// GitHub Actions check runs have ID > 0, so a link should be constructed
	if !strings.Contains(ciLaneComment, "**Link:**") {
		t.Errorf("expected CI lane link to include a job link, got: %q", ciLaneComment)
	}
	if !strings.Contains(ciLaneComment, gh.checkRuns[0].HTMLURL) {
		t.Errorf("expected CI lane link to include %q, got: %q", gh.checkRuns[0].HTMLURL, ciLaneComment)
	}
}

func TestProcessCIFailures_CommitStatusCILaneLink(t *testing.T) {
	// Issue #171: commit status entries (Prow) use target_url as the CI link.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test timed out intermittently on the Prow CI infrastructure"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{},
		commitStatuses: []CheckRun{
			// Commit status: ID=0, Output contains target_url
			{ID: 0, Name: "pull-unit-test", Status: "completed", Conclusion: "failure", Output: "Build failed\nhttps://prow.ci.kubevirt.io/view/gs/logs/1234"},
		},
		searchResults: []Issue{
			{Number: 999, Title: "Flaky CI: pull-unit-test", Labels: []string{"flaky-test"}},
		},
	}
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Should have 2 comments:
	// 1. CI lane link on the flaky issue (#999) — per-check side effect
	// 2. consolidated comment on PR
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (CI lane link + consolidated), got %d", len(gh.addedComments))
	}

	ciLaneComment := gh.addedComments[0]
	if !strings.Contains(ciLaneComment, "https://prow.ci.kubevirt.io/view/gs/logs/1234") {
		t.Errorf("expected CI lane link to include Prow URL, got: %q", ciLaneComment)
	}
}

func TestExtractURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://prow.ci.kubevirt.io/view/gs/logs/1234", "https://prow.ci.kubevirt.io/view/gs/logs/1234"},
		{"Build failed\nhttps://prow.ci.kubevirt.io/view/gs/logs/1234", "https://prow.ci.kubevirt.io/view/gs/logs/1234"},
		{"Build failed", ""},
		{"", ""},
		{"Error: timeout http://example.com/logs more text", "http://example.com/logs"},
		// Trailing punctuation is trimmed
		{"Check logs at https://example.com/log.", "https://example.com/log"},
		{"See https://example.com/log, then retry", "https://example.com/log"},
		{"See https://example.com/log) for details", "https://example.com/log"},
	}
	for _, tt := range tests {
		got := extractURL(tt.input)
		if got != tt.want {
			t.Errorf("extractURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestProcessCIFailures_ReinvestigatesAfterNewCommits(t *testing.T) {
	// Issue #28: Agent should re-investigate CI failures when new commits are pushed,
	// even if a previous rebase comment mentions the new commit SHA.
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed CI"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "tests failed"},
		},
		prHeadSHAs: []string{"abc1234", "def5678"}, // First call returns abc1234, second returns def5678
		issueComments: []ReviewComment{
			// Simulate a rebase comment that mentions the new commit
			{ID: 1, User: "test-bot", Body: "Rebased commit def5678 on main and pushed.\n\n<!-- oompa-bot -->"},
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent, func(w *IssueWork) {
		w.LastCheckedCISHA = "abc1234" // Already investigated abc1234
	})

	// First call: should skip because LastCheckedCISHA matches current HEAD (abc1234)
	agent.ProcessCIFailures(context.Background())
	if countCalls(runner.calls, "claude") != 0 {
		t.Errorf("expected 0 claude calls (same SHA), got %d", countCalls(runner.calls, "claude"))
	}

	// Second call: new commit def5678 pushed (e.g., by a human after rebase)
	// Even though there's a rebase comment mentioning def5678, the agent should
	// still investigate CI failures on this new commit
	agent.ProcessCIFailures(context.Background())
	if countCalls(runner.calls, "claude") != 1 {
		t.Errorf("expected 1 claude call (new SHA with CI failure), got %d", countCalls(runner.calls, "claude"))
	}
}

func TestProcessCIFailures_SkipsAlreadyReportedAfterRestart(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "tests failed"},
		},
		prHeadSHAs: []string{"abc1234567890"},
		issueComments: []ReviewComment{
			{ID: 1, User: "bot", Body: fmt.Sprintf("CI check `test` failed on commit abc1234 but appears unrelated to this PR's changes.\n\nFlaky test\n\n%s", ciMarker("abc1234567890", "test"))},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	if countCalls(runner.calls, "claude") != 0 {
		t.Errorf("expected 0 claude calls (already reported via comment), got %d", countCalls(runner.calls, "claude"))
	}
	if agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].LastCheckedCISHA != "abc1234567890" {
		t.Errorf("expected LastCheckedCISHA to be recovered to abc1234567890, got %q", agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].LastCheckedCISHA)
	}
}

func TestProcessCIFailures_NoDuplicateCommentsOnRepeatedPolls(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED Flaky network test"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "e2e-network", Status: "completed", Conclusion: "failure", Output: "timeout"},
		},
		prHeadSHAs: []string{"deadbeef1234567"},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment after first poll, got %d", len(gh.addedComments))
	}

	// Simulate the comment being visible on subsequent polls
	gh.issueComments = []ReviewComment{
		{ID: 1, User: "bot", Body: gh.addedComments[0]},
	}
	// Reset prHeadSHAs so mock returns the same SHA again
	gh.prHeadSHAs = []string{"deadbeef1234567"}

	// Second poll — should NOT post another comment
	agent.ProcessCIFailures(context.Background())

	if len(gh.addedComments) != 1 {
		t.Errorf("expected no new comments after second poll, got %d total", len(gh.addedComments))
	}
	if countCalls(runner.calls, "claude") != 1 {
		t.Errorf("expected 1 claude call total (skip second), got %d", countCalls(runner.calls, "claude"))
	}
}

func TestProcessCIFailures_DeduplicatesUnrelatedComments(t *testing.T) {
	// Issue #63: Should only post one unrelated comment per SHA, not on every poll cycle
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED Flaky test"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "tests failed"},
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	// First poll cycle: should investigate and post consolidated comment
	agent.ProcessCIFailures(context.Background())
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment on first poll, got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "Unrelated:") || !strings.Contains(gh.addedComments[0], "<code>test</code>") {
		t.Errorf("unexpected comment body: %s", gh.addedComments[0])
	}

	// Verify state was updated
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCheckedCISHA != "abc123" {
		t.Errorf("expected LastCheckedCISHA to be abc123, got %q", work.LastCheckedCISHA)
	}
	if work.LastCIStatus != "unrelated-failure" {
		t.Errorf("expected LastCIStatus to be unrelated-failure, got %q", work.LastCIStatus)
	}

	// Second poll cycle: same SHA, CI still failing
	// Should skip investigation entirely (no Claude call, no comment)
	agent.ProcessCIFailures(context.Background())
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected still only 1 comment after second poll (no duplicate), got %d", len(gh.addedComments))
	}
	// Verify Claude was not called again
	if countCalls(runner.calls, "claude") != 1 {
		t.Errorf("expected only 1 claude call total, got %d", countCalls(runner.calls, "claude"))
	}
}

func TestProcessCIFailures_DetectsCommitStatusFailures(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed CI"})
	gh := &mockGitHubClient{
		// No check run failures
		checkRuns: []CheckRun{
			{ID: 1, Name: "DCO", Status: "completed", Conclusion: "success"},
		},
		// Commit status failures (Prow)
		commitStatuses: []CheckRun{
			{Name: "pull-kubernetes-nmstate-unit-test", Status: "completed", Conclusion: "failure", Output: "https://prow.ci.kubevirt.io/logs/1234"},
		},
		prHeadSHAs: []string{"sha-before", "sha-after"},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	claudeCalls := countCalls(runner.calls, "claude")
	if claudeCalls != 1 {
		t.Fatalf("expected 1 claude call for commit status failure, got %d", claudeCalls)
	}
	if agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].CIFixAttempts != 1 {
		t.Errorf("expected 1 CI fix attempt, got %d", agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].CIFixAttempts)
	}
}

func TestProcessCIFailures_MergesCheckRunsAndCommitStatuses(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed both failures"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "github-actions-test", Status: "completed", Conclusion: "failure", Output: "test failed"},
		},
		commitStatuses: []CheckRun{
			{Name: "pull-unit-test", Status: "completed", Conclusion: "failure", Output: "https://prow.ci/logs/1234"},
		},
		prHeadSHAs: []string{"sha-before", "sha-after"},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Each failure should get its own Claude invocation for independent classification
	var claudePrompts []string
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudePrompts = append(claudePrompts, c.Stdin)
		}
	}
	if len(claudePrompts) != 2 {
		t.Fatalf("expected 2 claude calls (one per failure), got %d", len(claudePrompts))
	}

	// Verify each failure gets its own prompt
	foundCheckRun := false
	foundCommitStatus := false
	for _, prompt := range claudePrompts {
		if strings.Contains(prompt, "github-actions-test") {
			foundCheckRun = true
		}
		if strings.Contains(prompt, "pull-unit-test") {
			foundCommitStatus = true
		}
	}
	if !foundCheckRun {
		t.Errorf("expected one prompt to contain check run failure 'github-actions-test'")
	}
	if !foundCommitStatus {
		t.Errorf("expected one prompt to contain commit status failure 'pull-unit-test'")
	}
}

func TestProcessCIFailures_SkipChecksExcludesFromFailures(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "can-be-merged", Status: "completed", Conclusion: "failure", Output: "merge gate failed"},
			{ID: 2, Name: "unit-tests", Status: "completed", Conclusion: "failure", Output: "test failed"},
		},
	}
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED this is flaky"})
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipChecks = []string{"can-be-merged"}
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Only the non-skipped check should be investigated
	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
			// Verify the prompt does NOT mention the skipped check
			if strings.Contains(c.Stdin, "can-be-merged") {
				t.Error("skipped check 'can-be-merged' should not appear in claude prompt")
			}
		}
	}
	if claudeCalls != 1 {
		t.Fatalf("expected 1 claude call for non-skipped check, got %d", claudeCalls)
	}
}

func TestProcessCIFailures_SkipChecksAllFailuresSkipped(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "can-be-merged", Status: "completed", Conclusion: "failure", Output: "merge gate failed"},
			{ID: 2, Name: "verified", Status: "completed", Conclusion: "failure", Output: "not verified"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipChecks = []string{"can-be-merged", "verified"}
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// No claude calls since all failures are skipped
	if len(runner.calls) != 0 {
		t.Errorf("expected no claude calls when all failures are skipped, got %d", len(runner.calls))
	}
}

func TestProcessCIFailures_SkipChecksDoesNotAffectAllCompleted(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "can-be-merged", Status: "in_progress", Conclusion: ""},
			{ID: 2, Name: "unit-tests", Status: "completed", Conclusion: "success"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipChecks = []string{"can-be-merged"}
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// The skipped in_progress check should not prevent allCompleted from being true.
	// With can-be-merged skipped, only unit-tests (completed+success) remains,
	// so allCompleted=true and LastCheckedCISHA should be set.
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCheckedCISHA != "abc123" {
		t.Errorf("expected LastCheckedCISHA to be set when skipped check is the only non-completed, got %q", work.LastCheckedCISHA)
	}
}

func TestProcessCIFailures_ConsolidatesMultipleFailuresIntoSingleComment(t *testing.T) {
	// Issue #173: Multiple CI failures on the same SHA should produce a single consolidated comment.
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test-deploy", Status: "completed", Conclusion: "failure", Output: "GitHub git server returned HTTP 500"},
			{ID: 2, Name: "check-license-header", Status: "completed", Conclusion: "failure", Output: "GitHub git server returned HTTP 500"},
			{ID: 3, Name: "e2e-dual-conversion", Status: "completed", Conclusion: "failure", Output: "GitHub git server returned HTTP 500"},
		},
		checkRunLogs: map[int64]string{
			1: "Cloning repository...\nfatal: unable to access: HTTP 500 Internal Server Error",
			2: "Cloning repository...\nfatal: unable to access: HTTP 500 Internal Server Error",
			3: "Cloning repository...\nfatal: unable to access: HTTP 500 Internal Server Error",
		},
	}
	// All three failures are INFRASTRUCTURE
	infraResult := streamResultJSON(AgentResult{Result: "INFRASTRUCTURE GitHub git server returned HTTP 500"})
	runner := &mockCommandRunner{stdout: infraResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// All three failures investigated independently
	claudeCalls := countCalls(runner.calls, "claude")
	if claudeCalls != 3 {
		t.Fatalf("expected 3 claude calls (one per failure), got %d", claudeCalls)
	}

	// Only ONE consolidated comment should be posted
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}

	comment := gh.addedComments[0]
	// Should mention all three checks
	if !strings.Contains(comment, "test-deploy") {
		t.Errorf("expected comment to mention test-deploy")
	}
	if !strings.Contains(comment, "check-license-header") {
		t.Errorf("expected comment to mention check-license-header")
	}
	if !strings.Contains(comment, "e2e-dual-conversion") {
		t.Errorf("expected comment to mention e2e-dual-conversion")
	}
	// Should have infrastructure section with grouped count in collapsible details
	if !strings.Contains(comment, "Infrastructure (3)") {
		t.Errorf("expected Infrastructure (3) in grouped details, got: %q", comment)
	}
	// Should have per-check dedup markers
	if !strings.Contains(comment, ciMarker("abc123", "test-deploy")) {
		t.Errorf("expected per-check dedup marker for test-deploy")
	}
	if !strings.Contains(comment, ciMarker("abc123", "check-license-header")) {
		t.Errorf("expected per-check dedup marker for check-license-header")
	}
}

func TestProcessCIFailures_ConsolidatesMixedCategories(t *testing.T) {
	// Issue #173: Mixed categories (infrastructure + unrelated + related) in one consolidated comment.
	infraResult := streamResultJSON(AgentResult{Result: "INFRASTRUCTURE GitHub git server returned HTTP 500"})
	unrelatedResult := streamResultJSON(AgentResult{Result: "UNRELATED BGP peering timeout in e2e test"})
	relatedResult := streamResultJSON(AgentResult{Result: "RELATED Test assertion failed in kubevirt handler"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test-deploy", Status: "completed", Conclusion: "failure", Output: "GitHub git server returned HTTP 500 with a bunch of other text to make it 50 chars"},
			{ID: 2, Name: "e2e-bgp", Status: "completed", Conclusion: "failure", Output: "BGP peering timeout in e2e test with more detail padding to exceed fifty characters"},
			{ID: 3, Name: "e2e-control-plane", Status: "completed", Conclusion: "failure", Output: "Test assertion failed in kubevirt handler extra padding for the fifty char check"},
		},
		prHeadSHAs: []string{"sha-before", "sha-after"},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{infraResult, unrelatedResult, relatedResult}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Should have exactly ONE consolidated comment on the PR
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}

	comment := gh.addedComments[0]
	// All three categories should be present in collapsible details
	if !strings.Contains(comment, "Infrastructure:") {
		t.Errorf("expected Infrastructure details section, got: %q", comment)
	}
	if !strings.Contains(comment, "Unrelated:") {
		t.Errorf("expected Unrelated details section, got: %q", comment)
	}
	if !strings.Contains(comment, "Related:") {
		t.Errorf("expected Related details section, got: %q", comment)
	}
	// Summary header should have category breakdown
	if !strings.Contains(comment, "1 infrastructure, 1 unrelated, 1 related") {
		t.Errorf("expected category breakdown in summary, got: %q", comment)
	}
	// Related section should mention the fix was pushed
	if !strings.Contains(comment, "Pushed a fix") {
		t.Errorf("expected 'Pushed a fix' note, got: %q", comment)
	}
}

func TestProcessCIFailures_SingleFailureStillConsolidated(t *testing.T) {
	// Issue #173: A single failure should still use the consolidated format.
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED Flaky network test with detailed explanation exceeding fifty characters for the output check"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "e2e-network", Status: "completed", Conclusion: "failure", Output: "timeout connecting to service with some extra text to exceed the threshold"},
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(gh.addedComments))
	}
	comment := gh.addedComments[0]
	// Should use the structured format with summary header and collapsible details
	if !strings.Contains(comment, "CI Failure Analysis") {
		t.Errorf("expected structured format header, got: %q", comment)
	}
	if !strings.Contains(comment, "Unrelated:") {
		t.Errorf("expected Unrelated details section, got: %q", comment)
	}
	if !strings.Contains(comment, "<code>e2e-network</code>") {
		t.Errorf("expected check name in details summary, got: %q", comment)
	}
}

func TestProcessCIFailures_ConsolidatedSkipsInfrastructureSection(t *testing.T) {
	// Issue #173: skip-comment ci-infrastructure should suppress the infrastructure section
	// but still include other sections.
	infraResult := streamResultJSON(AgentResult{Result: "INFRASTRUCTURE GitHub git server returned HTTP 500"})
	unrelatedResult := streamResultJSON(AgentResult{Result: "UNRELATED Flaky test timeout exceeding the minimum chars check for the fifty character threshold"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test-deploy", Status: "completed", Conclusion: "failure", Output: "GitHub git server returned HTTP 500 with a bunch of extra text to be above threshold"},
			{ID: 2, Name: "e2e-bgp", Status: "completed", Conclusion: "failure", Output: "BGP peering timeout in test with a bunch of extra padding for length threshold"},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{infraResult, unrelatedResult}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipComments = []string{"ci-infrastructure"}
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	// Should have exactly 1 consolidated comment
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}

	comment := gh.addedComments[0]
	// Infrastructure section should be absent (skipped by config)
	if strings.Contains(comment, "Infrastructure:") || strings.Contains(comment, "Infrastructure (") {
		t.Errorf("expected no Infrastructure section (skipped), got: %q", comment)
	}
	// Unrelated section should be present
	if !strings.Contains(comment, "Unrelated:") {
		t.Errorf("expected Unrelated details section, got: %q", comment)
	}
	// Infrastructure check's dedup marker should still be present
	if !strings.Contains(comment, ciMarker("abc123", "test-deploy")) {
		t.Errorf("expected dedup marker for skipped infrastructure check")
	}
}

func TestProcessCIFailures_FlakyIssueLinkInConsolidatedComment(t *testing.T) {
	// Issue #173: Flaky issue links should appear in the unrelated section table.
	ciResult1 := streamResultJSON(AgentResult{Result: "UNRELATED Flaky test timeout exceeding the minimum chars check for the fifty character threshold"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout with additional text for the fifty character threshold"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{
			{Number: 42, Title: "Flaky CI: integration-tests", Labels: []string{"flaky-test"}},
		},
	}
	runner := &mockCommandRunner{stdout: ciResult1}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false
	agent.state.ActiveIssues[IssueKey("owner", "repo", 99)] = &IssueWork{
		IssueNumber:  99,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-99",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Should have 2 comments: CI lane link on flaky issue + consolidated on PR
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(gh.addedComments))
	}

	// Consolidated comment should include the flaky issue reference in the details block
	consolidated := gh.addedComments[1]
	if !strings.Contains(consolidated, "flaky test (#42)") {
		t.Errorf("expected flaky issue (#42) in details summary, got: %q", consolidated)
	}
	if !strings.Contains(consolidated, "Known Issue") {
		t.Errorf("expected Known Issue section, got: %q", consolidated)
	}
	if !strings.Contains(consolidated, "#42") {
		t.Errorf("expected flaky issue #42 reference, got: %q", consolidated)
	}
}

func TestProcessCIFailures_RelatedPushedFixNoteInConsolidated(t *testing.T) {
	// Issue #173: When a fix is pushed for a related failure, the consolidated
	// comment should note "Pushed a fix for the related failure."
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed the kubevirt handler test assertion"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "e2e-control-plane", Status: "completed", Conclusion: "failure", Output: "Test assertion failed in kubevirt handler extra text to exceed fifty characters"},
		},
		prHeadSHAs: []string{"sha-before", "sha-after"}, // different SHAs = fix pushed
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent)

	agent.ProcessCIFailures(context.Background())

	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}

	comment := gh.addedComments[0]
	if !strings.Contains(comment, "Related:") {
		t.Errorf("expected Related details section, got: %q", comment)
	}
	if !strings.Contains(comment, "fix pushed") {
		t.Errorf("expected 'fix pushed' in details summary, got: %q", comment)
	}
	if !strings.Contains(comment, "Pushed a fix") {
		t.Errorf("expected pushed fix note, got: %q", comment)
	}
}

func TestFirstSentence(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"GitHub git server returned HTTP 500. Detailed analysis follows.", "GitHub git server returned HTTP 500."},
		{"Simple explanation", "Simple explanation"},
		{"", ""},
		{"First line\nSecond line", "First line"},
		{strings.Repeat("x", 200), strings.Repeat("x", 120) + "..."},
	}
	for _, tt := range tests {
		got := firstSentence(tt.input)
		if got != tt.want {
			t.Errorf("firstSentence(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
