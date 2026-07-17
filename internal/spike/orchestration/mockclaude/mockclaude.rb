#!/usr/bin/env ruby
# frozen_string_literal: true

# Spike TDS-2: a stand-in for Claude's role in the tmux-driven contract. It runs
# as an interactive program in a tmux pane, reads typed lines from stdin, and
# for each `RUN <prompt> <out> <nonce>` line it emulates what we ask real Claude
# to do: read the prompt file, write a JSON answer to the output file, then print
# the completion sentinel `DONE-<nonce>`. This lets the Go harness validate the
# send-keys / capture-pane / completion-detection mechanics deterministically,
# without spending real Claude tokens. See docs/spikes/tds-2-tmux-orchestration.md.

require "json"

$stdout.sync = true
puts "READY" # readiness marker the harness waits for before sending work

STDIN.each_line do |line|
  line = line.strip
  next if line.empty?

  if line == "QUIT"
    break
  elsif line.start_with?("RUN ")
    _, prompt_path, out_path, done_path, nonce = line.split(" ", 5)
    task = (File.read(prompt_path).strip rescue "")
    File.write(out_path, JSON.generate({ "ok" => true, "nonce" => nonce, "task" => task }))
    File.write(done_path, "") # completion marker, written AFTER the payload
    puts "DONE-#{nonce}"       # also echoed to the pane for observability
  end
end
