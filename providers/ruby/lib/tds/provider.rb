# frozen_string_literal: true

require "json"
require_relative "structure"

module TDS
  # Provider is the tds Ruby provider: a resident process speaking the tds
  # provider protocol v1 (JSONL over stdio) — one request object per line in,
  # one response object per line out. stderr is reserved for logs.
  # See docs/protocol.md.
  class Provider
    PROTOCOL = "1.0.0"
    VERSION  = "0.1.0"

    # run reads requests until stdin closes, answering each on stdout.
    def run(input = $stdin, output = $stdout)
      output.sync = true
      input.each_line do |line|
        line = line.strip
        next if line.empty?

        response =
          begin
            handle(JSON.parse(line))
          rescue JSON::ParserError => e
            error(nil, "invalid_params", "invalid JSON: #{e.message}")
          end
        output.puts(JSON.generate(response))
      end
    end

    # handle dispatches one decoded request to a response envelope.
    def handle(request)
      id = request["id"]
      case request["op"]
      when "capabilities"
        success(id, capabilities)
      when "structure"
        success(id, Structure.new.run(request["params"] || {}))
      else
        error(id, "unsupported_op", "unsupported op: #{request['op'].inspect}")
      end
    rescue StandardError => e
      error(request["id"], "internal", "#{e.class}: #{e.message}")
    end

    def capabilities
      {
        "protocol"         => PROTOCOL,
        "provider"         => "tds-provider-ruby",
        "provider_version" => VERSION,
        "languages"        => ["ruby"],
        "operations"       => %w[capabilities structure],
        "analyzers"        => [] # analyze op arrives in TDS-28/29
      }
    end

    private

    def success(id, result)
      { "id" => id, "ok" => true, "result" => result }
    end

    def error(id, code, message)
      { "id" => id, "ok" => false, "error" => { "code" => code, "message" => message } }
    end
  end
end
