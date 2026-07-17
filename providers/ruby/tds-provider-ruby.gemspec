# frozen_string_literal: true

Gem::Specification.new do |spec|
  spec.name        = "tds-provider-ruby"
  spec.version     = "0.1.0"
  spec.authors     = ["tour-de-source"]
  spec.summary     = "Ruby/Rails structure & analysis provider for tour-de-source (tds)"
  spec.description = "Speaks the tds provider protocol v1: extracts symbols, imports, " \
                     "and Rails entrypoints via prism, and (later) runs Ruby analyzers."
  spec.license = "MIT"
  spec.required_ruby_version = ">= 3.4" # prism ships as a default gem

  spec.files       = Dir["lib/**/*.rb", "exe/*", "README.md"]
  spec.bindir      = "exe"
  spec.executables = ["tds-provider-ruby"]
  spec.require_paths = ["lib"]

  spec.add_development_dependency "minitest", "~> 5.0"
  spec.add_development_dependency "rake", "~> 13.0"
end
