# frozen_string_literal: true

require_relative "lib/supermetry/version"

Gem::Specification.new do |spec|
  spec.name = "supermetry"
  spec.version = Supermetry::VERSION
  spec.authors = ["Supermetry"]
  spec.summary = "Server-side SDK for Supermetry, self-hosted web analytics"
  spec.description = "Send pageviews, custom events, revenue and identify calls to a " \
                     "Supermetry server from Ruby. No runtime dependencies."
  spec.homepage = "https://supermetry.com"
  spec.license = "Apache-2.0"
  spec.required_ruby_version = ">= 3.0"

  spec.files = Dir["lib/**/*.rb", "README.md"]
  spec.require_paths = ["lib"]

  spec.metadata["rubygems_mfa_required"] = "true"

  spec.add_development_dependency "minitest", ">= 5.16"
  spec.add_development_dependency "rake", ">= 13"
end
