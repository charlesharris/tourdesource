# frozen_string_literal: true

require "prism"
require "digest"

module TDS
  # Structure extracts the tds "structure" payload (symbols, imports, Rails
  # entrypoints) from a batch of Ruby files using prism. It is the substance
  # behind the provider's `structure` operation. See docs/protocol.md.
  class Structure
    # run implements the structure operation over params {root, files}.
    def run(params)
      root = params["root"] || "."
      files = params["files"] || []
      out = { "symbols" => [], "imports" => [], "entrypoints" => [], "file_errors" => [] }

      files.each do |rel|
        src = read(File.join(root, rel))
        unless src
          out["file_errors"] << { "path" => rel, "message" => "cannot read #{rel}" }
          next
        end
        parse = Prism.parse(src)
        unless parse.errors.empty?
          out["file_errors"] << { "path" => rel, "message" => parse.errors.map(&:message).join("; ") }
        end
        collector = SymbolCollector.new(rel)
        parse.value.accept(collector) # best-effort even on parse errors
        out["symbols"].concat(collector.symbols)
        out["imports"].concat(collector.imports)
        out["entrypoints"].concat(EntrypointDetector.detect(rel, collector.classes))
      end

      out
    end

    private

    def read(path)
      File.read(path)
    rescue SystemCallError
      nil
    end
  end

  # SymbolCollector walks a prism AST accumulating class/module/method symbols
  # with normalized qualified paths, require edges, and the class list used for
  # Rails entrypoint detection.
  class SymbolCollector < Prism::Visitor
    attr_reader :symbols, :imports, :classes

    def initialize(rel)
      @rel = rel
      @scope = []
      @symbols = []
      @imports = []
      @classes = []
    end

    def visit_class_node(node)
      name = node.constant_path.slice
      qualified = (@scope + [name]).join("::")
      @symbols << build("class", name, qualified, node)
      @classes << { "name" => qualified, "superclass" => node.superclass&.slice, "path" => @rel }
      @scope.push(name)
      super
      @scope.pop
    end

    def visit_module_node(node)
      name = node.constant_path.slice
      qualified = (@scope + [name]).join("::")
      @symbols << build("module", name, qualified, node)
      @scope.push(name)
      super
      @scope.pop
    end

    def visit_def_node(node)
      # `#` for instance methods, `.` for singleton (`def self.x` / `def obj.x`).
      sep = node.receiver ? "." : "#"
      qualified = @scope.empty? ? node.name.to_s : "#{@scope.join('::')}#{sep}#{node.name}"
      @symbols << build("method", node.name.to_s, qualified, node)
      super
    end

    def visit_call_node(node)
      if (node.name == :require || node.name == :require_relative) && node.arguments
        arg = node.arguments.arguments.first
        if arg.is_a?(Prism::StringNode)
          @imports << { "path" => @rel, "target" => arg.unescaped, "kind" => node.name.to_s }
        end
      end
      super
    end

    private

    def build(kind, name, qualified, node)
      {
        "path" => @rel,
        "kind" => kind,
        "name" => name,
        "symbol" => qualified,
        "start_line" => node.location.start_line,
        "end_line" => node.location.end_line,
        "body_hash" => body_hash(node)
      }
    end

    # body_hash hashes the symbol's normalized source (trailing whitespace and
    # leading/trailing blank lines stripped) so cosmetic edits don't register as
    # drift while real changes do (design §5.3).
    def body_hash(node)
      lines = node.slice.lines.map(&:rstrip)
      lines.shift while lines.first == ""
      lines.pop while lines.last == ""
      "sha256:#{Digest::SHA256.hexdigest(lines.join("\n"))}"
    end
  end

  # EntrypointDetector classifies Rails entrypoints by superclass first, then by
  # path/name convention. Heuristic by design (design §6.1).
  module EntrypointDetector
    RAILS_BASES = {
      "ApplicationRecord"     => "rails-model",
      "ActiveRecord::Base"    => "rails-model",
      "ApplicationController" => "rails-controller",
      "ActionController::Base" => "rails-controller",
      "ApplicationJob"        => "rails-job",
      "ActiveJob::Base"       => "rails-job",
      "ApplicationMailer"     => "rails-mailer",
      "ActionMailer::Base"    => "rails-mailer"
    }.freeze

    def self.detect(rel, classes)
      eps = []
      eps << { "path" => rel, "kind" => "rails-routes" } if rel == "config/routes.rb" || rel.end_with?("/config/routes.rb")
      classes.each do |c|
        kind = kind_for(c["superclass"], rel, c["name"])
        eps << { "path" => rel, "kind" => kind, "name" => c["name"] } if kind
      end
      eps
    end

    def self.kind_for(superclass, rel, name)
      return RAILS_BASES[superclass] if superclass && RAILS_BASES.key?(superclass)
      return "rails-controller" if rel.include?("app/controllers/") && name.end_with?("Controller")
      return "rails-job" if rel.match?(%r{(^|/)app/jobs/})
      return "rails-model" if rel.match?(%r{(^|/)app/models/}) && !rel.include?("app/models/concerns/")

      nil
    end
  end
end
