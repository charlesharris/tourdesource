# A Rails-style fixture model for the tds Ruby provider tests.
require_relative "invoice_calculations"

class Invoice < ApplicationRecord
  belongs_to :account
  has_many :line_items

  def finalize
    raise "already finalized" if finalized?

    with_lock { update!(status: :finalized) }
  end

  def finalized?
    status == "finalized"
  end

  def self.overdue
    where("due_on < ?", Date.today)
  end
end
