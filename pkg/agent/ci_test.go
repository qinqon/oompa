package agent

import (
	"strings"
	"testing"
)

func TestParseCIStructuredFields_AllFields(t *testing.T) {
	input := `ERROR_SUMMARY: test assertion failed in kubevirt handler

ROOT_CAUSE: The PR changed GenerateNetworkOverrideMachineConfig() but didn't update the test fixture to include the new nmstate config fields.

EVIDENCE:
TestKubeVirtHandler/should_generate_nmstate_config:
  Expected: "autoconf: false"
  Got: ""

RECOMMENDATION: Update test fixtures to include new nmstate config fields.

FAILING_TEST: TestKubeVirtHandler/should_generate_nmstate_config`

	errorSummary, rootCause, evidence, recommendation, failingTest := parseCIStructuredFields(input)

	if errorSummary != "test assertion failed in kubevirt handler" {
		t.Errorf("errorSummary = %q, want %q", errorSummary, "test assertion failed in kubevirt handler")
	}
	if !strings.Contains(rootCause, "GenerateNetworkOverrideMachineConfig") {
		t.Errorf("rootCause missing expected content, got %q", rootCause)
	}
	if !strings.Contains(evidence, "autoconf: false") {
		t.Errorf("evidence missing expected content, got %q", evidence)
	}
	if !strings.Contains(recommendation, "Update test fixtures") {
		t.Errorf("recommendation missing expected content, got %q", recommendation)
	}
	if failingTest != "TestKubeVirtHandler/should_generate_nmstate_config" {
		t.Errorf("failingTest = %q, want %q", failingTest, "TestKubeVirtHandler/should_generate_nmstate_config")
	}
}

func TestParseCIStructuredFields_MissingFields(t *testing.T) {
	input := `Some preamble text.

ERROR_SUMMARY: HTTP 500 from git server

ROOT_CAUSE: GitHub's git server returned HTTP 500 during clone.`

	errorSummary, rootCause, evidence, recommendation, failingTest := parseCIStructuredFields(input)

	if errorSummary != "HTTP 500 from git server" {
		t.Errorf("errorSummary = %q, want %q", errorSummary, "HTTP 500 from git server")
	}
	if !strings.Contains(rootCause, "HTTP 500") {
		t.Errorf("rootCause = %q, expected to contain 'HTTP 500'", rootCause)
	}
	if evidence != "" {
		t.Errorf("evidence should be empty, got %q", evidence)
	}
	if recommendation != "" {
		t.Errorf("recommendation should be empty, got %q", recommendation)
	}
	if failingTest != "" {
		t.Errorf("failingTest should be empty, got %q", failingTest)
	}
}

func TestParseCIStructuredFields_NoFields(t *testing.T) {
	input := "GitHub git server returned HTTP 500. This is an infrastructure failure."

	errorSummary, rootCause, evidence, recommendation, failingTest := parseCIStructuredFields(input)

	if errorSummary != "" || rootCause != "" || evidence != "" || recommendation != "" || failingTest != "" {
		t.Errorf("expected all empty fields for unstructured input, got: summary=%q root=%q evidence=%q rec=%q test=%q",
			errorSummary, rootCause, evidence, recommendation, failingTest)
	}
}

func TestParseCIStructuredFields_EvidenceMultiLine(t *testing.T) {
	input := `ERROR_SUMMARY: test timeout

EVIDENCE:
=== RUN TestNetworkPolicy
    timeout after 300s
    waiting for pod readiness
--- FAIL: TestNetworkPolicy (300.12s)

RECOMMENDATION: Retest with /retest`

	_, _, evidence, recommendation, _ := parseCIStructuredFields(input)

	if !strings.Contains(evidence, "TestNetworkPolicy") {
		t.Errorf("evidence missing test name, got %q", evidence)
	}
	if !strings.Contains(evidence, "timeout after 300s") {
		t.Errorf("evidence missing timeout message, got %q", evidence)
	}
	// Evidence should stop at the next field
	if strings.Contains(evidence, "RECOMMENDATION") {
		t.Errorf("evidence should not contain RECOMMENDATION field, got %q", evidence)
	}
	if recommendation != "Retest with /retest" {
		t.Errorf("recommendation = %q, want %q", recommendation, "Retest with /retest")
	}
}

func TestParseCIStructuredFields_ClassificationLineSkipped(t *testing.T) {
	input := `CLASSIFICATION: INFRASTRUCTURE

ERROR_SUMMARY: DNS resolution failure

ROOT_CAUSE: Cluster DNS was unavailable.`

	errorSummary, rootCause, _, _, _ := parseCIStructuredFields(input)

	if errorSummary != "DNS resolution failure" {
		t.Errorf("errorSummary = %q, want %q", errorSummary, "DNS resolution failure")
	}
	if rootCause != "Cluster DNS was unavailable." {
		t.Errorf("rootCause = %q, want %q", rootCause, "Cluster DNS was unavailable.")
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

func TestFormatCIRelatedDetails(t *testing.T) {
	r := ciResult{
		check:          "unit-tests",
		category:       "related",
		errorSummary:   "test assertion failed",
		rootCause:      "PR changed the handler but test fixture was not updated.",
		evidence:       "Expected: \"autoconf: false\"\nGot: \"\"",
		recommendation: "Update test fixtures.",
		pushed:         true,
	}

	output := formatCIRelatedDetails(r)

	if !strings.Contains(output, "<details>") {
		t.Errorf("expected collapsible details block")
	}
	if !strings.Contains(output, "🔴 Related") {
		t.Errorf("expected Related emoji marker")
	}
	if !strings.Contains(output, "<code>unit-tests</code>") {
		t.Errorf("expected check name in code block")
	}
	if !strings.Contains(output, "fix pushed") {
		t.Errorf("expected 'fix pushed' in summary")
	}
	if !strings.Contains(output, "### Error") {
		t.Errorf("expected Error section")
	}
	if !strings.Contains(output, "autoconf: false") {
		t.Errorf("expected evidence in Error section")
	}
	if !strings.Contains(output, "### Root Cause") {
		t.Errorf("expected Root Cause section")
	}
	if !strings.Contains(output, "handler but test fixture") {
		t.Errorf("expected root cause content")
	}
	if !strings.Contains(output, "### Action") {
		t.Errorf("expected Action section for pushed fix")
	}
	if !strings.Contains(output, "</details>") {
		t.Errorf("expected closing details tag")
	}
}

func TestFormatCIRelatedDetails_NotPushed(t *testing.T) {
	r := ciResult{
		check:          "lint",
		category:       "related",
		errorSummary:   "lint failure",
		recommendation: "Fix lint errors.",
	}

	output := formatCIRelatedDetails(r)

	if !strings.Contains(output, "fix needed") {
		t.Errorf("expected 'fix needed' in summary for non-pushed fix")
	}
	if strings.Contains(output, "### Action") {
		t.Errorf("should not have Action section when fix was not pushed")
	}
}

func TestFormatCIUnrelatedDetails(t *testing.T) {
	r := ciResult{
		check:          "e2e-conformance",
		category:       "unrelated",
		errorSummary:   "SCTP ingress test timeout",
		rootCause:      "This test has been flaking independently due to cluster network latency.",
		evidence:       "TestNetworkPolicyV2 — timeout after 300s",
		recommendation: "Skip or quarantine the test.",
		failingTest:    "TestNetworkPolicyV2",
		flakyIssue:     6381,
	}
	agent := &Agent{cfg: Config{}}

	output := formatCIUnrelatedDetails(r, agent)

	if !strings.Contains(output, "<details>") {
		t.Errorf("expected collapsible details block")
	}
	if !strings.Contains(output, "⚠️ Unrelated") {
		t.Errorf("expected Unrelated emoji marker")
	}
	if !strings.Contains(output, "flaky test (#6381)") {
		t.Errorf("expected flaky issue reference in summary")
	}
	if !strings.Contains(output, "### Known Issue") {
		t.Errorf("expected Known Issue section")
	}
	if !strings.Contains(output, "#6381") {
		t.Errorf("expected issue reference")
	}
}

func TestFormatCIUnrelatedDetails_NoFlakyIssue(t *testing.T) {
	r := ciResult{
		check:        "e2e-network",
		category:     "unrelated",
		errorSummary: "network timeout",
	}
	agent := &Agent{cfg: Config{}}

	output := formatCIUnrelatedDetails(r, agent)

	if !strings.Contains(output, "flaky test") {
		t.Errorf("expected 'flaky test' label")
	}
	if strings.Contains(output, "Known Issue") {
		t.Errorf("should not have Known Issue section without flaky issue")
	}
}

func TestFormatCIInfrastructureSection_Grouped(t *testing.T) {
	infra := []ciResult{
		{check: "test-deploy", category: "infrastructure", errorSummary: "HTTP 500 from git server", rootCause: "GitHub outage", recommendation: "Retest with /retest"},
		{check: "check-license-header", category: "infrastructure", errorSummary: "HTTP 500 from git server"},
		{check: "e2e-dual-conversion", category: "infrastructure", errorSummary: "HTTP 500 from git server"},
	}

	output := formatCIInfrastructureSection(infra)

	if !strings.Contains(output, "🔧 Infrastructure (3)") {
		t.Errorf("expected grouped infrastructure header with count 3")
	}
	if !strings.Contains(output, "HTTP 500 from git server") {
		t.Errorf("expected error summary in header")
	}
	// All checks should appear in the table
	if !strings.Contains(output, "`test-deploy`") {
		t.Errorf("expected test-deploy in table")
	}
	if !strings.Contains(output, "`check-license-header`") {
		t.Errorf("expected check-license-header in table")
	}
	if !strings.Contains(output, "`e2e-dual-conversion`") {
		t.Errorf("expected e2e-dual-conversion in table")
	}
	// Should use root cause from first result
	if !strings.Contains(output, "GitHub outage") {
		t.Errorf("expected root cause from first result")
	}
	if !strings.Contains(output, "Retest with /retest") {
		t.Errorf("expected recommendation from first result")
	}
}

func TestFormatCIInfrastructureSection_SingleCheck(t *testing.T) {
	infra := []ciResult{
		{check: "build", category: "infrastructure", errorSummary: "OOM killed", rootCause: "Runner ran out of memory."},
	}

	output := formatCIInfrastructureSection(infra)

	if !strings.Contains(output, "🔧 Infrastructure: <code>build</code>") {
		t.Errorf("expected single infrastructure check format")
	}
	if !strings.Contains(output, "OOM killed") {
		t.Errorf("expected error summary")
	}
	if !strings.Contains(output, "Runner ran out of memory") {
		t.Errorf("expected root cause")
	}
}

func TestFormatCIInfrastructureSection_MixedErrors(t *testing.T) {
	infra := []ciResult{
		{check: "test-deploy", category: "infrastructure", errorSummary: "HTTP 500 from git server"},
		{check: "e2e-bgp", category: "infrastructure", errorSummary: "HTTP 500 from git server"},
		{check: "build", category: "infrastructure", errorSummary: "OOM killed"},
	}

	output := formatCIInfrastructureSection(infra)

	// Should have two groups: one with 2 (HTTP 500) and one single (OOM)
	if !strings.Contains(output, "Infrastructure (2)") {
		t.Errorf("expected grouped header with count 2")
	}
	if !strings.Contains(output, "Infrastructure: <code>build</code>") {
		t.Errorf("expected single check format for OOM")
	}
}

func TestResultSummaryLine_PrefersErrorSummary(t *testing.T) {
	r := ciResult{
		errorSummary: "structured summary",
		explanation:  "Full explanation with many details. Second sentence.",
	}
	got := resultSummaryLine(r)
	if got != "structured summary" {
		t.Errorf("resultSummaryLine = %q, want %q", got, "structured summary")
	}
}

func TestResultSummaryLine_FallsBackToExplanation(t *testing.T) {
	r := ciResult{
		explanation: "Full explanation with many details. Second sentence.",
	}
	got := resultSummaryLine(r)
	if got != "Full explanation with many details." {
		t.Errorf("resultSummaryLine = %q, want %q", got, "Full explanation with many details.")
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

func TestWriteStructuredBody_AllFields(t *testing.T) {
	var b strings.Builder
	r := ciResult{
		evidence:       "line 1\nline 2",
		rootCause:      "Something broke.",
		recommendation: "Fix it.",
	}
	writeStructuredBody(&b, "summary line", r)
	output := b.String()

	if !strings.Contains(output, "### Error") {
		t.Errorf("expected Error section")
	}
	if !strings.Contains(output, "line 1\nline 2") {
		t.Errorf("expected evidence content")
	}
	if !strings.Contains(output, "### Root Cause") {
		t.Errorf("expected Root Cause section")
	}
	if !strings.Contains(output, "### Recommendation") {
		t.Errorf("expected Recommendation section")
	}
}

func TestWriteStructuredBody_NoEvidence_UsesSummary(t *testing.T) {
	var b strings.Builder
	r := ciResult{}
	writeStructuredBody(&b, "fallback summary", r)
	output := b.String()

	if !strings.Contains(output, "fallback summary") {
		t.Errorf("expected summary as fallback in Error section")
	}
}

func TestWriteStructuredBody_Empty(t *testing.T) {
	var b strings.Builder
	r := ciResult{}
	writeStructuredBody(&b, "", r)
	output := b.String()

	if strings.Contains(output, "### Error") {
		t.Errorf("should not have Error section with empty summary and evidence")
	}
	if strings.Contains(output, "### Root Cause") {
		t.Errorf("should not have Root Cause section with empty rootCause")
	}
}

func TestFormatCIRelatedDetails_HTMLEscapesCheckName(t *testing.T) {
	r := ciResult{
		check:        "pull-ci-<org>/repo-e2e",
		category:     "related",
		errorSummary: "test failed",
		pushed:       false,
	}

	output := formatCIRelatedDetails(r)

	// Check name should be HTML-escaped inside <code> tags
	if !strings.Contains(output, "&lt;org&gt;") {
		t.Errorf("expected HTML-escaped check name, got: %s", output)
	}
	if strings.Contains(output, "<org>") {
		t.Errorf("check name with < and > should be escaped, got: %s", output)
	}
}

func TestFormatCIUnrelatedDetails_HTMLEscapesCheckName(t *testing.T) {
	r := ciResult{
		check:        "e2e-<shard>&test",
		category:     "unrelated",
		errorSummary: "flaky timeout",
	}
	agent := &Agent{cfg: Config{}}

	output := formatCIUnrelatedDetails(r, agent)

	if !strings.Contains(output, "&lt;shard&gt;&amp;test") {
		t.Errorf("expected HTML-escaped check name, got: %s", output)
	}
}

func TestFormatCIInfrastructureSection_HTMLEscapesSingleCheck(t *testing.T) {
	infra := []ciResult{
		{check: "build-<arch>", category: "infrastructure", errorSummary: "OOM killed"},
	}

	output := formatCIInfrastructureSection(infra)

	if !strings.Contains(output, "&lt;arch&gt;") {
		t.Errorf("expected HTML-escaped check name in single infra block, got: %s", output)
	}
}

func TestWriteFenced_NoBackticks(t *testing.T) {
	var b strings.Builder
	writeFenced(&b, "### Error", "plain error text")
	output := b.String()

	if !strings.Contains(output, "```\nplain error text\n```") {
		t.Errorf("expected standard triple-backtick fence, got: %s", output)
	}
}

func TestWriteFenced_WithBackticks(t *testing.T) {
	var b strings.Builder
	body := "Error in ```yaml\nkey: value\n```"
	writeFenced(&b, "### Error", body)
	output := b.String()

	// Should use a longer fence (4 backticks) to avoid breakout
	if !strings.Contains(output, "````") {
		t.Errorf("expected longer fence for body with backticks, got: %s", output)
	}
	// Should contain the body unchanged
	if !strings.Contains(output, body) {
		t.Errorf("expected body to be preserved unchanged, got: %s", output)
	}
}

func TestWriteFenced_WithLongBacktickRun(t *testing.T) {
	var b strings.Builder
	body := "Some `````long````` backtick run"
	writeFenced(&b, "### Error", body)
	output := b.String()

	// Should use fence longer than 5 (the longest run)
	if !strings.Contains(output, "``````") {
		t.Errorf("expected 6+ backtick fence, got: %s", output)
	}
}

func TestParseCIStructuredFields_EvidenceWithFieldLikeLines(t *testing.T) {
	// Evidence block contains indented lines that look like field headers.
	// These should NOT terminate evidence capture because they're not at column 0.
	input := `ERROR_SUMMARY: test failure

EVIDENCE:
  test output:
    RECOMMENDATION: some test recommendation output
    ROOT_CAUSE: some diagnostic line
  more test output

RECOMMENDATION: Retest with /retest`

	_, _, evidence, recommendation, _ := parseCIStructuredFields(input)

	// The indented field-like lines should be captured as evidence
	if !strings.Contains(evidence, "RECOMMENDATION: some test recommendation output") {
		t.Errorf("expected indented RECOMMENDATION to be captured as evidence, got %q", evidence)
	}
	if !strings.Contains(evidence, "ROOT_CAUSE: some diagnostic line") {
		t.Errorf("expected indented ROOT_CAUSE to be captured as evidence, got %q", evidence)
	}
	// The actual RECOMMENDATION field (at column 0) should still be parsed
	if recommendation != "Retest with /retest" {
		t.Errorf("recommendation = %q, want %q", recommendation, "Retest with /retest")
	}
}
