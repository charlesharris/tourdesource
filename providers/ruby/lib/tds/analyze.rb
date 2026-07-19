# frozen_string_literal: true

require "json"
require "open3"
require "shellwords"

module TDS
  # Analyze implements the provider protocol's `analyze` operation: it runs the
  # Ruby ecosystem's analyzers and normalizes their output into tds findings.
  #
  # Each analyzer is a small class that knows four things: how to find its tool,
  # what version that tool is, how to run it, and how to turn its output into
  # findings. Adding one is adding a class to REGISTRY.
  #
  # Failure is always per-analyzer. A missing tool, a config error, or a crash
  # yields an `analyzer_errors` entry and the other analyzers still run — a
  # half-analyzed repo is useful, a failed request is not.
  class Analyze
    # REGISTRY is the analyzers this provider offers, in report order.
    def self.registry
      [Analyzers::Rubocop.new, Analyzers::Brakeman.new,
       Analyzers::Flog.new, Analyzers::SimpleCov.new, Analyzers::Sorbet.new]
    end

    # capabilities describes each analyzer for the handshake, including whether
    # its tool is actually installed. An unavailable analyzer is still
    # advertised — the core reports what *would* run, which is more useful than
    # silently omitting it.
    def self.capabilities(root = Dir.pwd)
      registry.map do |a|
        info = {
          "name" => a.name,
          "tool" => a.tool,
          "available" => a.available?(root),
          "views" => a.views,
          "incremental" => a.incremental?
        }
        if info["available"] && (v = a.tool_version(root))
          info["tool_version"] = v
        end
        info
      end
    end

    # run implements the analyze operation over params {root, files, analyzers}.
    def run(params)
      root = params["root"] || Dir.pwd
      files = params["files"] || []
      only = params["analyzers"]
      out = { "findings" => [], "analyzer_errors" => [] }

      self.class.registry.each do |analyzer|
        next if only && !only.empty? && !only.include?(analyzer.name)

        unless analyzer.available?(root)
          out["analyzer_errors"] << {
            "analyzer" => analyzer.name,
            "message" => analyzer.unavailable_reason(root)
          }
          next
        end

        begin
          out["findings"].concat(analyzer.run(root: root, files: files))
        rescue StandardError => e
          out["analyzer_errors"] << {
            "analyzer" => analyzer.name,
            "message" => "#{e.class}: #{e.message}"
          }
        end
      end

      out
    end
  end

  module Analyzers
    # Base handles the part every analyzer shares: locating its tool.
    #
    # Tool resolution prefers the project's own bundle, because a repo that pins
    # rubocop 1.81 means it — a globally installed 1.87 reports different
    # offenses. But `bundle exec` is not reliable in a Rails app: a .rubocop.yml
    # that requires rubocop-rails can boot the application, and Redmine's fails
    # outright without a database.yml. So the bundle is *tried* and the global
    # binary is the fallback, rather than either being assumed.
    class Base
      # Views this analyzer's findings render in (design §8).
      def views = []

      # Whether this analyzer's findings for a file depend only on that file, so
      # the core may cache them per file (TDS-26). False by default: that is the
      # answer that can only cost time, never correctness.
      def incremental? = false

      def name = raise(NotImplementedError)
      def tool = name

      # command returns the argv prefix that runs the tool in root, or nil.
      def command(root)
        @command ||= {}
        return @command[root] if @command.key?(root)

        @command[root] = resolve_command(root)
      end

      def available?(root) = !command(root).nil?

      # Why this analyzer did not run. The default is a missing binary; an
      # analyzer that depends on something else says so, because "simplecov is
      # not installed" sends someone to install a gem when what they actually
      # need is to run their tests.
      def unavailable_reason(_root) = "#{tool} is not installed"

      def tool_version(root)
        @tool_version ||= {}
        return @tool_version[root] if @tool_version.key?(root)

        @tool_version[root] = detect_version(root)
      end

      def run(root:, files:) = raise(NotImplementedError)

      private

      # version_flag is how this tool reports its version.
      def version_flag = "--version"

      # Bundler exports a set of variables that pin a child process to *our*
      # bundle. That is the wrong bundle: a globally installed analyzer then
      # fails with "rubocop is not currently included in the bundle". Guessing
      # at the variable list does not work — clearing the obvious ones still
      # leaves BUNDLER_SETUP and GEM_HOME to re-activate it.
      #
      # Bundler snapshots each original value in BUNDLER_ORIG_<NAME> for exactly
      # this purpose, so restoring from those is the supported way to get back
      # the environment the user actually has. This sentinel means "was unset".
      BUNDLER_NIL = "BUNDLER_ENVIRONMENT_PRESERVER_INTENTIONALLY_NIL"
      BUNDLER_PREFIXES = %w[BUNDLE_ BUNDLER_].freeze

      def resolve_command(root)
        candidates = []
        # The project's own bundle first: a repo that pins rubocop 1.81 means it.
        candidates << ["bundle", "exec", tool] if File.exist?(File.join(root, "Gemfile"))
        candidates << [tool]

        candidates.find do |argv|
          _out, status = capture(argv + [version_flag], root)
          status&.success?
        end
      end

      # env_for builds the environment a tool runs in: the caller's own
      # environment with any surrounding bundle undone. For a `bundle exec`
      # invocation the project's Gemfile is then named explicitly, so it uses
      # the repo's bundle rather than whatever bundle launched this provider.
      def env_for(argv, root)
        env = unbundled_env
        env["BUNDLE_GEMFILE"] = File.join(root, "Gemfile") if argv.first == "bundle"
        env
      end

      # unbundled_env returns the environment as it was before Bundler touched
      # it: every BUNDLER_ORIG_X restored to X, and Bundler's own variables
      # removed. Returns nil-valued keys for variables that must be unset.
      def unbundled_env
        env = {}
        ENV.each_key do |key|
          if (orig = key[/\ABUNDLER_ORIG_(.+)\z/, 1])
            value = ENV[key]
            env[orig] = value == BUNDLER_NIL ? nil : value
            env[key] = nil
          elsif BUNDLER_PREFIXES.any? { |p| key.start_with?(p) }
            env[key] = nil
          end
        end
        env
      end

      def detect_version(root)
        argv = command(root)
        return nil unless argv

        out, status = capture(argv + [version_flag], root)
        return nil unless status&.success?

        # Tools print either a bare version or "name x.y.z"; take the first
        # thing that looks like a version.
        out[/\d+\.\d+(\.\d+)?/]
      end

      # capture runs argv in root, returning [stdout, status]. A missing binary
      # is a nil status rather than an exception, so callers can treat "not
      # installed" as data.
      def capture(argv, root)
        Open3.popen3(env_for(argv, root), *argv, chdir: root) do |stdin, stdout, stderr, wait|
          stdin.close
          out = stdout.read
          stderr.read
          [out, wait.value]
        end
      rescue Errno::ENOENT, Errno::EACCES
        [nil, nil]
      end

      # run_tool runs argv in root and returns stdout, ignoring the exit status.
      # Linters exit non-zero when they find offenses, which is success for us.
      def run_tool(argv, root)
        Open3.popen3(env_for(argv, root), *argv, chdir: root) do |stdin, stdout, stderr, wait|
          stdin.close
          out = stdout.read
          err = stderr.read
          status = wait.value
          # An empty payload with a bad status is a real failure (bad config,
          # crash) rather than "clean run with offenses".
          raise "#{tool} failed (exit #{status.exitstatus}): #{err.strip[0, 300]}" if out.strip.empty? && !status.success?

          out
        end
      end

      # relative normalizes a tool's path to a repo-relative slash path, so
      # findings key against the same paths the map uses.
      def relative(path, root)
        return nil if path.nil? || path.empty?

        p = path.start_with?("/") ? path : File.join(root, path)
        rel = begin
          Pathname.new(p).relative_path_from(Pathname.new(root)).to_s
        rescue StandardError
          path
        end
        rel.start_with?("..") ? path : rel
      end
    end

    # Rubocop reports style and lint offenses as inline annotations.
# Flog scores method complexity. It is a *measurement*, not a defect report:
    # a high score is a reason to look, not evidence of a bug. Findings are
    # therefore all informational and carry the score in `value`, which is what
    # the heatmap renders. Calling a complex method an error would be asserting
    # a judgement flog does not make.
    class Flog < Base
      # Report only methods at or above this score. Flog's community scale puts
      # 0-10 at "awesome" and 11-20 at "good enough"; below ~25 the output is
      # every method in the repository, which is a heatmap of nothing.
      MIN_SCORE = 25.0
      FILES_PER_RUN = 250

      # "  116.4: TimeEntry#validate_time_entry    app/models/time_entry.rb:164-193"
      #
      # The symbol is NOT \S+. Minitest generates method names containing
      # spaces — "GanttsControllerTest::test#renders chart with selected start
      # month and year" is a real one from Redmine — so a greedy first token
      # steals part of the name and the rest lands in the path. The location is
      # anchored at the end instead, and the symbol is whatever precedes it.
      #
      # The location is also optional: flog buckets code outside any method
      # under "Class#none" with no file, and prints two header lines with no
      # path at all.
      LINE = /\A\s*([\d.]+):\s+(.+?)(?:\s+(\S+):(\d+)(?:-(\d+))?)?\s*\z/.freeze

      def name = "flog"
      def views = %w[heatmap badge]
      def incremental? = true

      def run(root:, files:)
        ruby = files.select { |f| f.end_with?(".rb", ".rake") }
        return [] if ruby.empty?

        version = tool_version(root)
        findings = []

        ruby.each_slice(FILES_PER_RUN) do |slice|
          # -a reports every method rather than only the worst offenders; the
          # threshold is ours to apply, so we want the full list to apply it to.
          payload = run_tool(command(root) + ["-a"] + slice, root)
          payload.each_line do |line|
            m = LINE.match(line)
            next unless m

            score, symbol, path, start_line, end_line = m.captures
            # No path means a header total or flog's out-of-method bucket:
            # neither points at code a reader can open.
            next if path.nil?

            score = score.to_f
            next if score < MIN_SCORE

            findings << {
              "path" => relative(path, root),
              "start_line" => start_line.to_i,
              "end_line" => (end_line || start_line).to_i,
              "symbol" => symbol,
              "severity" => "info",
              "rule" => "complexity",
              "message" => format("Flog score %.1f for %s.", score, symbol),
              "tool" => "flog",
              "tool_version" => version,
              "view" => "heatmap",
              "value" => score
            }.compact
          end
        end

        findings
      end

      private

      # flog answers --version with the literal string "flog: version unknown"
      # and exits non-zero, so the base class's probe would both fail to get a
      # version and conclude the tool is missing. -h succeeds and proves the
      # executable runs.
      def version_flag = "-h"

      # The library knows its version even though the CLI does not, and a view
      # without provenance is weaker than one with it.
      public def tool_version(root)
        @tool_version ||= {}
        return @tool_version[root] if @tool_version.key?(root)

        @tool_version[root] = flog_version(root)
      end

      def flog_version(root)
        out, status = capture(["ruby", "-e", "require 'flog'; print Flog::VERSION"], root)
        return nil unless status&.success?

        out[/\d+\.\d+(\.\d+)?/]
      end
    end

    # SimpleCov coverage. Unlike the other analyzers this runs no tool: coverage
    # only exists if the project's own test suite has been run, and running a
    # repository's tests as a side effect of building a tour would be a
    # startling thing for `tds analyze` to do. It reads the report SimpleCov
    # already wrote, and is simply unavailable when there is not one.
    class SimpleCov < Base
      RESULTSET = "coverage/.resultset.json"

      def name = "simplecov"
      def tool = "simplecov"
      def views = %w[heatmap badge]

      # Availability is the artifact's existence, not a binary on PATH.
      def available?(root) = File.exist?(File.join(root, RESULTSET))
      def command(root) = nil
      def tool_version(root) = nil

      def unavailable_reason(_root)
        "no coverage report at #{RESULTSET} — run the project's test suite with " \
          "SimpleCov enabled, then analyze again"
      end

      def run(root:, files:)
        path = File.join(root, RESULTSET)
        return [] unless File.exist?(path)

        data = JSON.parse(File.read(path))
        wanted = files.each_with_object({}) { |f, h| h[f] = true }
        findings = []

        data.each_value do |suite|
          coverage = suite.is_a?(Hash) ? suite["coverage"] : nil
          next unless coverage.is_a?(Hash)

          coverage.each do |abs, entry|
            rel = relative(abs, root)
            next unless wanted[rel]

            # SimpleCov has two shapes: a bare line array (older) and
            # {"lines" => [...]} (current). nil means "not relevant" — a blank
            # line or a comment — and must not count against the file.
            lines = entry.is_a?(Hash) ? entry["lines"] : entry
            next unless lines.is_a?(Array)

            relevant = lines.count { |c| !c.nil? }
            next if relevant.zero?

            covered = lines.count { |c| !c.nil? && c.positive? }
            pct = (covered.to_f / relevant * 100).round(1)

            findings << {
              "path" => rel,
              "start_line" => 1,
              "end_line" => lines.length,
              "severity" => "info",
              "rule" => "coverage",
              "message" => format("%.1f%% line coverage (%d of %d relevant lines).",
                                  pct, covered, relevant),
              "tool" => "simplecov",
              "view" => "heatmap",
              "value" => pct
            }
          end
        end

        findings
      end
    end

    # Sorbet type checking.
    #
    # UNVERIFIED against real output: sorbet was not installed when this was
    # written and no repository to hand carries a sorbet/ config, so the parser
    # below is built from sorbet's documented error format rather than from a
    # captured run. It is availability-gated on both the binary and the config,
    # so it stays dormant until someone has both — at which point this comment
    # is the first thing to check if the findings look wrong.
    class Sorbet < Base
      # "app/models/foo.rb:12: Method `bar` does not exist on `Foo` https://srb.help/7003"
      LINE = %r{\A(?<path>[^\s:]+):(?<line>\d+):\s*(?<message>.+?)(?:\s+(?<url>https://srb\.help/(?<code>\d+)))?\s*\z}.freeze

      def name = "sorbet"
      def tool = "srb"
      def views = %w[annotations panel]

      # A sorbet binary with no sorbet/config in the repository would type-check
      # nothing and report a config error, which is noise rather than a finding.
      def available?(root)
        super && File.exist?(File.join(root, "sorbet", "config"))
      end

      def run(root:, files:)
        return [] unless available?(root)

        version = tool_version(root)
        # srb tc exits non-zero when it finds errors, which is the normal case.
        # run_tool only treats that as a failure when there is no output at all.
        payload = run_tool(command(root) + ["tc", "--no-color"], root)
        findings = []

        payload.each_line do |line|
          m = LINE.match(line.strip)
          next unless m

          rel = relative(m[:path], root)
          findings << {
            "path" => rel,
            "start_line" => m[:line].to_i,
            "end_line" => m[:line].to_i,
            "severity" => "error",
            "rule" => m[:code] ? "srb-#{m[:code]}" : "type",
            "message" => m[:message],
            "url" => m[:url],
            "tool" => "sorbet",
            "tool_version" => version,
            "view" => "annotations"
          }.compact
        end

        findings
      end

      private

      def version_flag = "--version"
    end

    class Rubocop < Base
      # ARG_MAX is finite and a Rails app can have thousands of Ruby files, so
      # invocations are chunked rather than passing every path at once.
      FILES_PER_RUN = 250

      def name = "rubocop"
      def views = ["annotations"]
      def incremental? = true

      def run(root:, files:)
        ruby = files.select { |f| f.end_with?(".rb", ".rake", ".gemspec") }
        return [] if ruby.empty?

        version = tool_version(root)
        findings = []

        ruby.each_slice(FILES_PER_RUN) do |slice|
          # --force-exclusion honours the project's own exclude list even though
          # we name files explicitly; without it we would report offenses in
          # vendored code the project has deliberately opted out of.
          argv = command(root) + ["--format", "json", "--force-exclusion"] + slice
          payload = run_tool(argv, root)
          next if payload.strip.empty?

          data = JSON.parse(payload)
          version ||= data.dig("metadata", "rubocop_version")

          (data["files"] || []).each do |file|
            path = relative(file["path"], root)
            (file["offenses"] || []).each do |o|
              loc = o["location"] || {}
              findings << {
                "path" => path,
                "start_line" => loc["start_line"] || loc["line"] || 1,
                "end_line" => loc["last_line"] || loc["start_line"] || loc["line"] || 1,
                "severity" => severity_for(o["severity"]),
                "rule" => o["cop_name"],
                "message" => o["message"],
                "url" => nil,
                "tool" => "rubocop",
                "tool_version" => version,
                "view" => "annotations"
              }.compact
            end
          end
        end

        findings
      end

      private

      # rubocop's severities are finer than the protocol's error|warning|info.
      def severity_for(s)
        case s
        when "fatal", "error" then "error"
        when "warning" then "warning"
        else "info" # convention, refactor
        end
      end
    end

    # Brakeman reports security warnings. They render as a browsable panel
    # rather than inline annotations: a security review is a list you work
    # through, not a margin note.
    class Brakeman < Base
      def name = "brakeman"
      def views = %w[panel annotations]

      def run(root:, files:)
        # Brakeman analyses the whole application — it follows data flow across
        # files, so it cannot be scoped to a batch. Run it once and keep the
        # warnings that land in files the core asked about.
        argv = command(root) + ["-f", "json", "-q", "--no-pager", "."]
        payload = run_tool(argv, root)
        return [] if payload.strip.empty?

        data = JSON.parse(payload)
        version = data.dig("scan_info", "brakeman_version") || tool_version(root)
        wanted = files.empty? ? nil : files.to_set

        (data["warnings"] || []).filter_map do |w|
          path = relative(w["file"], root)
          next if wanted && !wanted.include?(path)

          line = w["line"] || 1
          {
            "path" => path,
            "start_line" => line,
            "end_line" => line,
            "severity" => severity_for(w["confidence"]),
            "rule" => w["warning_type"],
            "message" => w["message"],
            "url" => w["link"],
            "tool" => "brakeman",
            "tool_version" => version,
            "view" => "panel"
          }.compact
        end
      end

      private

      # Brakeman states confidence, not severity. Treating a high-confidence
      # finding as an error and a weak one as informational is the mapping that
      # keeps the security panel useful rather than alarming.
      def severity_for(confidence)
        case confidence
        when "High" then "error"
        when "Medium" then "warning"
        else "info" # Weak
        end
      end
    end
  end
end
