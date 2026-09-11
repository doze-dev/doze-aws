// order-validator — Node.js 22, ESM.
//
// First step of the order-fulfilment workflow: decide whether an order is one
// Harbour can accept at all, before anything downstream spends money on it.
// Rejections are returned, not thrown, so the state machine can route them to
// a Choice rather than a Catch — a rejected order is an outcome, not a failure.

const MAX_BASKET_GBP = 400;
const SERVICEABLE = new Set(['EH', 'G', 'KY', 'FK', 'ML']);

/** "EH8 9YL" -> "EH". Postcode areas are letters up to the first digit. */
function area(postcode) {
  const m = /^([A-Z]{1,2})\d/.exec((postcode ?? '').toUpperCase().trim());
  return m ? m[1] : '';
}

export const handler = async (event, context) => {
  const order = event.order ?? event;
  const reasons = [];

  if (!Array.isArray(order.lines) || order.lines.length === 0) {
    reasons.push('the basket is empty');
  }
  const total = (order.lines ?? []).reduce((sum, l) => sum + l.unitPriceGbp * l.quantity, 0);
  if (total > MAX_BASKET_GBP) {
    reasons.push(`basket total £${total.toFixed(2)} is over the £${MAX_BASKET_GBP} single-order limit`);
  }
  const postArea = area(order.deliverTo?.postcode);
  if (!SERVICEABLE.has(postArea)) {
    reasons.push(`we do not deliver to ${order.deliverTo?.postcode ?? 'an unknown postcode'} yet`);
  }
  for (const line of order.lines ?? []) {
    if (line.quantity > 24) {
      reasons.push(`${line.name}: ${line.quantity} exceeds the 24-per-line cap`);
    }
  }

  console.log(
    `validating ${order.orderId} for ${order.customer?.name ?? 'unknown'} — ` +
      `${(order.lines ?? []).length} lines, £${total.toFixed(2)}, ${postArea || '??'}`
  );
  if (reasons.length) console.warn(`rejected ${order.orderId}: ${reasons.join('; ')}`);

  return {
    ...order,
    validation: {
      accepted: reasons.length === 0,
      reasons,
      subtotalGbp: Number(total.toFixed(2)),
      deliveryArea: postArea,
      checkedBy: `order-validator (node ${process.version})`,
      requestId: context.awsRequestId,
    },
  };
};
