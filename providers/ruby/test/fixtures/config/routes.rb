Rails.application.routes.draw do
  resources :invoices
  post "/webhooks", to: "webhooks#create"
end
