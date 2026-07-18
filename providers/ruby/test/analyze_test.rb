# frozen_string_literal: true

require "minitest/autorun"
require "tmpdir"
require "json"
require "tds/analyze"

# These tests exercise normalization and orchestration without requiring the
# real tools to be installed: a fake analyzer stands in where the behaviour
# under test is Analyze's, and the normalizers are fed recorded tool output.
# The real tools are covered by an integration test that skips when absent.
class AnalyzeTest < Minitest::Test
  FIXTURES = File.expand_path("fixtures", __dir__)

  # --- normalization -------------------------------------------------------

  # Recorded from rubocop 1.87.0 on a real file.
  RUBOCOP_JSON = {
    "metadata" => { "rubocop_version" => "1.87.0" },
    "files" => [{
      "path" => "app/models/invoice.rb",
      "offenses" => [
        { "severity" => "convention", "cop_name" => "Style/ReduceToHash",
          "message" => "Use `to_h { ... }` instead of `inject`.",
          "location" => { "start_line" => 216, "last_line" => 216 } },
        { "severity" => "error", "cop_name" => "Lint/Syntax",
          "message" => "unexpected token",
          "location" => { "start_line" => 5, "last_line" => 7 } },
        { "severity" => "warning", "cop_name" => "Lint/UselessAssignment",
          "message" => "Useless assignment to `x`.",
          "location" => { "start_line" => 9, "last_line" => 9 } }
      ]
    }]
  }.freeze

  # A Rubocop analyzer that returns recorded output instead of shelling out.
  class FakeRubocop < TDS::Analyzers::Rubocop
    def initialize(payload) = (@payload = payload)
    def command(_root) = ["rubocop"]
    def tool_version(_root) = "1.87.0"
    def run_tool(_argv, _root) = @payload
  end

  def test_rubocop_offenses_normalize_to_findings
    findings = FakeRubocop.new(JSON.generate(RUBOCOP_JSON))
                          .run(root: "/repo", files: ["app/models/invoice.rb"])

    assert_equal 3, findings.size

    f = findings.first
    assert_equal "app/models/invoice.rb", f["path"]
    assert_equal 216, f["start_line"]
    assert_equal "Style/ReduceToHash", f["rule"]
    assert_equal "rubocop", f["tool"]
    assert_equal "1.87.0", f["tool_version"], "provenance must survive normalization"
    assert_equal "annotations", f["view"]
  end

  # The protocol's severity vocabulary is error|warning|info; rubocop's is
  # finer, so the mapping is part of the contract.
  def test_rubocop_severities_map_to_the_protocol_vocabulary
    findings = FakeRubocop.new(JSON.generate(RUBOCOP_JSON))
                          .run(root: "/repo", files: ["app/models/invoice.rb"])
    by_rule = findings.to_h { |f| [f["rule"], f["severity"]] }

    assert_equal "info", by_rule["Style/ReduceToHash"], "convention is informational"
    assert_equal "error", by_rule["Lint/Syntax"]
    assert_equal "warning", by_rule["Lint/UselessAssignment"]
    findings.each do |f|
      assert_includes %w[error warning info], f["severity"]
    end
  end

  def test_rubocop_multiline_offense_keeps_its_range
    findings = FakeRubocop.new(JSON.generate(RUBOCOP_JSON))
                          .run(root: "/repo", files: ["app/models/invoice.rb"])
    syntax = findings.find { |f| f["rule"] == "Lint/Syntax" }

    assert_equal 5, syntax["start_line"]
    assert_equal 7, syntax["end_line"]
  end

  def test_rubocop_skips_non_ruby_files
    called = false
    fake = FakeRubocop.new(JSON.generate(RUBOCOP_JSON))
    fake.define_singleton_method(:run_tool) { |*| called = true; "" }

    assert_empty fake.run(root: "/repo", files: %w[README.md app/views/x.html.erb])
    refute called, "rubocop should not be invoked when no Ruby files are requested"
  end

  # Recorded from brakeman 8.0.4 on Redmine.
  BRAKEMAN_JSON = {
    "scan_info" => { "brakeman_version" => "8.0.4" },
    "warnings" => [
      { "warning_type" => "SQL Injection", "confidence" => "High",
        "message" => "Possible SQL injection", "file" => "app/models/user.rb",
        "line" => 101, "link" => "https://brakemanscanner.org/docs/warning_types/sql_injection/" },
      { "warning_type" => "Dangerous Eval", "confidence" => "Weak",
        "message" => "Dynamic code evaluation", "file" => "app/models/user.rb", "line" => 302 },
      { "warning_type" => "Command Injection", "confidence" => "Medium",
        "message" => "Possible command injection", "file" => "lib/other.rb", "line" => 86 }
    ]
  }.freeze

  class FakeBrakeman < TDS::Analyzers::Brakeman
    def initialize(payload) = (@payload = payload)
    def command(_root) = ["brakeman"]
    def tool_version(_root) = "8.0.4"
    def run_tool(_argv, _root) = @payload
  end

  def test_brakeman_warnings_normalize_to_panel_findings
    findings = FakeBrakeman.new(JSON.generate(BRAKEMAN_JSON))
                           .run(root: "/repo", files: [])

    assert_equal 3, findings.size
    sql = findings.find { |f| f["rule"] == "SQL Injection" }
    assert_equal "app/models/user.rb", sql["path"]
    assert_equal 101, sql["start_line"]
    assert_equal "panel", sql["view"], "security findings render as a browsable panel"
    assert_equal "brakeman", sql["tool"]
    assert_equal "8.0.4", sql["tool_version"]
    assert_match %r{brakemanscanner\.org}, sql["url"]
  end

  # Brakeman states confidence, not severity.
  def test_brakeman_confidence_maps_to_severity
    findings = FakeBrakeman.new(JSON.generate(BRAKEMAN_JSON))
                           .run(root: "/repo", files: [])
    by_rule = findings.to_h { |f| [f["rule"], f["severity"]] }

    assert_equal "error", by_rule["SQL Injection"]      # High
    assert_equal "warning", by_rule["Command Injection"] # Medium
    assert_equal "info", by_rule["Dangerous Eval"]       # Weak
  end

  # Brakeman scans the whole app because it follows data flow across files, so
  # the requested batch is a filter applied afterwards rather than an argument.
  def test_brakeman_filters_to_requested_files
    findings = FakeBrakeman.new(JSON.generate(BRAKEMAN_JSON))
                           .run(root: "/repo", files: ["app/models/user.rb"])

    assert_equal 2, findings.size
    assert(findings.all? { |f| f["path"] == "app/models/user.rb" })
  end

  # --- orchestration -------------------------------------------------------

  class UnavailableAnalyzer < TDS::Analyzers::Base
    def name = "ghost"
    def tool = "ghost-tool"
    def available?(_root) = false
  end

  class ExplodingAnalyzer < TDS::Analyzers::Base
    def name = "boom"
    def available?(_root) = true
    def run(root:, files:) = raise("analyzer exploded")
  end

  class WorkingAnalyzer < TDS::Analyzers::Base
    def name = "fine"
    def available?(_root) = true
    def run(root:, files:)
      [{ "path" => "a.rb", "start_line" => 1, "end_line" => 1, "severity" => "info",
         "rule" => "ok", "message" => "fine", "tool" => "fine", "view" => "annotations" }]
    end
  end

  def with_registry(analyzers)
    TDS::Analyze.singleton_class.send(:alias_method, :real_registry, :registry)
    TDS::Analyze.define_singleton_method(:registry) { analyzers }
    yield
  ensure
    TDS::Analyze.singleton_class.send(:alias_method, :registry, :real_registry)
  end

  # A missing tool is data, not a failure: the core reports what would have run.
  def test_unavailable_analyzer_is_reported_not_fatal
    with_registry([UnavailableAnalyzer.new, WorkingAnalyzer.new]) do
      res = TDS::Analyze.new.run("root" => "/repo", "files" => ["a.rb"])

      assert_equal 1, res["findings"].size, "the working analyzer must still run"
      assert_equal 1, res["analyzer_errors"].size
      assert_equal "ghost", res["analyzer_errors"].first["analyzer"]
      assert_match(/not installed/, res["analyzer_errors"].first["message"])
    end
  end

  # One analyzer blowing up must not lose the others' results.
  def test_failing_analyzer_is_isolated
    with_registry([ExplodingAnalyzer.new, WorkingAnalyzer.new]) do
      res = TDS::Analyze.new.run("root" => "/repo", "files" => ["a.rb"])

      assert_equal 1, res["findings"].size
      assert_equal 1, res["analyzer_errors"].size
      assert_match(/exploded/, res["analyzer_errors"].first["message"])
    end
  end

  def test_analyzers_param_restricts_the_run
    with_registry([ExplodingAnalyzer.new, WorkingAnalyzer.new]) do
      res = TDS::Analyze.new.run("root" => "/repo", "files" => ["a.rb"], "analyzers" => ["fine"])

      assert_equal 1, res["findings"].size
      assert_empty res["analyzer_errors"], "the unselected analyzer should not have run"
    end
  end

  def test_capabilities_advertise_unavailable_analyzers
    with_registry([UnavailableAnalyzer.new]) do
      caps = TDS::Analyze.capabilities("/repo")

      assert_equal 1, caps.size
      assert_equal "ghost", caps.first["name"]
      refute caps.first["available"]
      refute caps.first.key?("tool_version"), "an absent tool has no version"
    end
  end

  # --- integration ---------------------------------------------------------

  # Runs the real rubocop against a fixture, proving the shell-out, the JSON
  # contract and the normalization line up. Skips when rubocop isn't installed
  # so the suite stays runnable everywhere.
  def test_real_rubocop_end_to_end
    analyzer = TDS::Analyzers::Rubocop.new
    skip "rubocop not installed" unless analyzer.available?(FIXTURES)

    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "offender.rb"), <<~RUBY)
        # frozen_string_literal: true
        def bad
          x = 1
          "unused assignment above"
        end
      RUBY

      findings = analyzer.run(root: dir, files: ["offender.rb"])

      refute_empty findings, "rubocop should report at least one offense here"
      findings.each do |f|
        assert_equal "offender.rb", f["path"], "paths must be repo-relative"
        assert_includes %w[error warning info], f["severity"]
        assert_equal "rubocop", f["tool"]
        assert f["start_line"].positive?
        assert_operator f["end_line"], :>=, f["start_line"]
      end
    end
  end
end
