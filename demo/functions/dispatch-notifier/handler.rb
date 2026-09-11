# dispatch-notifier — Ruby.
#
# Last step of the order-fulfilment workflow: turn a priced, picked order into
# the words a customer actually reads. The delivery note, the substitution
# apology, and the SMS that has to fit in one segment.
#
# Text handling is why this one is Ruby, and it is the kind of back-office job
# that stays in Ruby long after the rest of a shop has moved on.
#
# Standard library only.

require 'json'
require 'time'

SMS_SEGMENT = 160

# Harbour's delivery slots, as the warehouse labels them.
SLOTS = {
  'express' => 'today, 6pm–9pm',
  'standard' => 'tomorrow, 8am–12pm'
}.freeze

def money(pence)
  format('£%d.%02d', pence / 100, pence % 100)
end

def sentence(items)
  return items.first.to_s if items.length <= 1

  "#{items[0..-2].join(', ')} and #{items[-1]}"
end

def handler(event:, context:)
  order = event['orderId'] ? event : event.fetch('order', {})
  pricing = order['pricing'] || {}
  fulfilment = order['fulfilment'] || {}
  customer = order['customer'] || {}
  first_name = (customer['name'] || 'there').split(' ').first

  slot = SLOTS.fetch(order['deliverySpeed'], SLOTS['standard'])
  total = money(pricing.fetch('totalPence', 0))

  subs = (fulfilment['substitutions'] || []).map { |s| s['substitute'] }
  shorts = (fulfilment['shortLines'] || []).map { |s| s['sku'] }

  lines = []
  lines << "Hi #{first_name}, your Harbour order #{order['orderId']} is confirmed."
  lines << "Total #{total}, arriving #{slot}."
  unless subs.empty?
    lines << "We've swapped in #{sentence(subs)} — same price, and you can refuse it at the door."
  end
  unless shorts.empty?
    lines << "#{sentence(shorts)} sold out before we got to your basket; you have not been charged for it."
  end
  lines << 'Track it in the app. Reply STOP to opt out of delivery texts.'

  body = lines.join(' ')
  sms = body.length > SMS_SEGMENT ? "#{body[0, SMS_SEGMENT - 1]}…" : body

  puts "notified #{order['orderId']} — #{subs.length} substitutions, " \
       "#{shorts.length} short, #{(body.length / SMS_SEGMENT.to_f).ceil} SMS segments"

  order['notification'] = {
    'channel' => customer['mobile'] ? 'sms' : 'email',
    'to' => customer['mobile'] || customer['email'],
    'subject' => "Harbour order #{order['orderId']} — #{slot}",
    'sms' => sms,
    'body' => lines.join("\n\n"),
    'slot' => slot,
    'sentAt' => Time.now.utc.iso8601,
    'writtenBy' => "dispatch-notifier (ruby #{RUBY_VERSION})"
  }
  order
end
