class WebhooksController < ApplicationController
  def create
    Invoice.create!(webhook_params)
    head :ok
  end

  private

  def webhook_params
    params.permit(:account_id, :amount)
  end
end
