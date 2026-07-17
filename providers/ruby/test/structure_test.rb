# frozen_string_literal: true

require "minitest/autorun"
require "tds/provider"
require "tds/structure"

class StructureTest < Minitest::Test
  FIXTURES = File.expand_path("fixtures", __dir__)

  def structure(*files)
    TDS::Structure.new.run("root" => FIXTURES, "files" => files)
  end

  def symbols_by_name(result)
    result["symbols"].to_h { |s| [s["symbol"], s] }
  end

  def test_symbols_have_qualified_paths_and_ranges
    syms = symbols_by_name(structure("app/models/invoice.rb"))

    assert_equal "class", syms["Invoice"]["kind"]
    assert syms.key?("Invoice#finalize"), "instance method missing"
    assert syms.key?("Invoice#finalized?"), "predicate method missing"
    assert syms.key?("Invoice.overdue"), "singleton method should use '.' separator"

    inv = syms["Invoice"]
    assert_operator inv["start_line"], :<, inv["end_line"]
  end

  def test_body_hash_is_stable_and_prefixed
    first = symbols_by_name(structure("app/models/invoice.rb"))["Invoice#finalize"]["body_hash"]
    second = symbols_by_name(structure("app/models/invoice.rb"))["Invoice#finalize"]["body_hash"]

    assert_match(/\Asha256:[0-9a-f]{64}\z/, first)
    assert_equal first, second, "body_hash must be deterministic"
  end

  def test_requires_become_imports
    targets = structure("app/models/invoice.rb")["imports"].map { |i| i["target"] }

    assert_includes targets, "invoice_calculations"
  end

  def test_model_entrypoint_by_superclass
    kinds = structure("app/models/invoice.rb")["entrypoints"].map { |e| e["kind"] }

    assert_includes kinds, "rails-model"
  end

  def test_controller_entrypoint
    ep = structure("app/controllers/webhooks_controller.rb")["entrypoints"]
      .find { |e| e["kind"] == "rails-controller" }

    refute_nil ep
    assert_equal "WebhooksController", ep["name"]
  end

  def test_routes_entrypoint
    kinds = structure("config/routes.rb")["entrypoints"].map { |e| e["kind"] }

    assert_includes kinds, "rails-routes"
  end

  def test_unreadable_file_is_reported_not_fatal
    result = structure("app/models/does_not_exist.rb")

    assert_equal 1, result["file_errors"].length
    assert_empty result["symbols"]
  end

  def test_capabilities
    caps = TDS::Provider.new.capabilities

    assert_equal "1.0.0", caps["protocol"]
    assert_equal "tds-provider-ruby", caps["provider"]
    assert_includes caps["languages"], "ruby"
    assert_includes caps["operations"], "structure"
  end

  def test_handle_wraps_structure_in_envelope
    resp = TDS::Provider.new.handle(
      "id" => 5, "op" => "structure",
      "params" => { "root" => FIXTURES, "files" => ["app/models/invoice.rb"] }
    )

    assert_equal 5, resp["id"]
    assert resp["ok"]
    refute_empty resp["result"]["symbols"]
  end

  def test_unsupported_op_is_an_error_envelope
    resp = TDS::Provider.new.handle("id" => 1, "op" => "frobnicate")

    refute resp["ok"]
    assert_equal "unsupported_op", resp["error"]["code"]
  end
end
