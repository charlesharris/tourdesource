---
title: "A tour of Acme's billing service"
template: onboarding
repo: .
commit: auto
audience: "new backend engineers"
maintainer: "billing-team"
---

# Chapter: The 30-second version

Acme Billing turns usage events into invoices. Everything flows through
one queue and one state machine.

::stop{anchor="app/models/invoice.rb::Invoice" focus="def finalize"}
`Invoice` is the aggregate root. The whole domain is really "get an
Invoice from draft to finalized without double-charging."
::

# Chapter: Follow one invoice end to end

::stop{anchor="app/controllers/webhooks_controller.rb::WebhooksController#create"}
Usage events arrive here. Note we never touch the DB directly — we enqueue.
::

::stop{anchor="app/jobs/finalize_invoice_job.rb::FinalizeInvoiceJob#perform"}
The job is where the state machine actually turns.

::detour{title="If you're debugging a stuck invoice"}
Stuck invoices are almost always here — the lock in `with_lock` below.
::stop{anchor="app/models/invoice.rb::Invoice#with_lock"}
The lock is held for the whole finalize; a crash mid-finalize leaves it stuck.
::
::
::
