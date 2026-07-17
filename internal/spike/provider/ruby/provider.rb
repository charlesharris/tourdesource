#!/usr/bin/env ruby
# frozen_string_literal: true

# Spike TDS-3: a minimal tds Ruby provider. Speaks a draft JSON protocol over
# stdio (one request in on stdin, one response out on stdout) and answers two
# operations: `capabilities` and `structure` (via prism). Throwaway — the real
# provider + protocol is TDS-10 / TDS-5. See docs/spikes/tds-3-provider-protocol.md.

require "json"
require "prism"

PROTOCOL = "0.0.1-spike"

def capabilities
  {
    "protocol" => PROTOCOL,
    "provider" => "tds-provider-ruby",
    "languages" => ["ruby"],
    "operations" => ["capabilities", "structure"],
    "analyzers" => [], # stubbed for the spike
    "prism" => Prism::VERSION,
  }
end

# Walks a prism AST collecting class/module/method symbols with qualified paths
# (Module::Class, Class#method, Class.singleton) and 1-based line ranges.
class SymbolCollector < Prism::Visitor
  attr_reader :symbols

  def initialize(rel)
    @rel = rel
    @scope = []
    @symbols = []
  end

  def visit_class_node(node)
    enter(node, "class") { super }
  end

  def visit_module_node(node)
    enter(node, "module") { super }
  end

  def visit_def_node(node)
    sep = node.receiver ? "." : "#"
    qualified = @scope.empty? ? node.name.to_s : "#{@scope.join("::")}#{sep}#{node.name}"
    @symbols << build("method", node.name.to_s, qualified, node.location)
    super
  end

  private

  def enter(node, kind)
    name = node.constant_path.slice
    @symbols << build(kind, name, (@scope + [name]).join("::"), node.location)
    @scope.push(name)
    yield
    @scope.pop
  end

  def build(kind, name, qualified, loc)
    {
      "path" => @rel,
      "kind" => kind,
      "name" => name,
      "symbol" => qualified,
      "start_line" => loc.start_line,
      "end_line" => loc.end_line,
    }
  end
end

def structure(req)
  root = req["root"] || "."
  files = req["files"] || []
  symbols = []
  files.each do |rel|
    path = File.join(root, rel)
    next unless File.file?(path)
    collector = SymbolCollector.new(rel)
    Prism.parse(File.read(path)).value.accept(collector)
    symbols.concat(collector.symbols)
  end
  { "symbols" => symbols }
end

def dispatch(req)
  case req["op"]
  when "capabilities" then capabilities
  when "structure" then structure(req)
  else { "error" => "unknown op: #{req["op"].inspect}" }
  end
end

raw = $stdin.read
request = raw.strip.empty? ? {} : JSON.parse(raw)
$stdout.write(JSON.generate(dispatch(request)))
