@playback
Feature: Playback runtime serves canned results
  # Replays canned agent results without LLM inference.
  # Tests the harness infrastructure (dispatch, pre-scripts, post-scripts,
  # label application) using an ordered playlist of result directories.

  Scenario: Triage a feature request
    Given an installed test repository
    And a triage agent that returns "triage/feature"
    When an issue "add-csv-export" is created
    Then the triage agent completes successfully
    And the issue has a successful status comment
    And the issue is labeled with "triaged"
    And the issue is not labeled with "ready-to-code"

  Scenario: Triage a bug report
    Given an installed test repository
    And the installed agents are "triage"
    And a triage agent that returns "triage/bug"
    When an issue "pagination-off-by-one" is created
    Then the triage agent completes successfully
    And the issue has a successful status comment
    And the issue is labeled with "ready-to-code"
    And the issue is not labeled with "triaged"

  Scenario: Full end-to-end pipeline
    Given an installed test repository
    And a triage agent that returns "triage/bug"
    And a code agent that returns "code/bug-fix"
    And a review agent that returns "review/request-changes"
    And a fix agent that returns "fix/success"
    And a review agent that returns "review/approve"
    When an issue "add-file-download" is created
    Then the triage agent completes successfully
    And the issue is labeled with "ready-to-code"
    Then the code agent completes successfully
    And a pull request exists
    Then the review agent completes successfully
    And the review agent submitted "changes_requested"
    Then the fix agent completes successfully
    Then the review agent completes successfully
    And the review agent submitted "approved"
