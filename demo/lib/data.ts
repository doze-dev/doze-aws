// Harbour — the fictional grocery delivery company this seed builds.
//
// Everything here is invented but shaped like the real thing: Edinburgh and
// Glasgow postcodes because Harbour is a Scottish company that has not expanded
// yet, prices that look like a supermarket's, SKUs with the department prefix
// buyers actually use. The point is screenshots you can read — a console full
// of `test-bucket-1` and `{"foo":"bar"}` teaches nobody what the tool is for.
//
// No real people, no real addresses, no real card numbers. The phone numbers
// are in Ofcom's 07700 900xxx drama range and the domains are example.com.

export const COMPANY = 'harbour';

export const PRODUCTS = [
  { sku: 'BAK-SRD-800', name: 'Sourdough loaf, 800g', dept: 'Bakery', priceGbp: 3.85, vatable: false },
  { sku: 'BAK-CRS-4', name: 'Butter croissants, 4 pack', dept: 'Bakery', priceGbp: 2.5, vatable: false },
  { sku: 'DRY-OAT-1L', name: 'Oat drink, 1L', dept: 'Dairy & alternatives', priceGbp: 1.45, vatable: false, multibuy: '3for2' },
  { sku: 'DRY-BTR-250', name: 'Salted butter, 250g', dept: 'Dairy & alternatives', priceGbp: 2.35, vatable: false },
  { sku: 'DRY-CHE-200', name: 'Mature cheddar, 200g', dept: 'Dairy & alternatives', priceGbp: 3.1, vatable: false },
  { sku: 'PRD-TOM-400', name: 'Vine tomatoes, 400g', dept: 'Produce', priceGbp: 1.9, vatable: false },
  { sku: 'PRD-SPN-200', name: 'Baby spinach, 200g', dept: 'Produce', priceGbp: 1.6, vatable: false },
  { sku: 'PRD-APP-6', name: 'Braeburn apples, 6 pack', dept: 'Produce', priceGbp: 2.2, vatable: false },
  { sku: 'MEA-CHK-500', name: 'Free range chicken thighs, 500g', dept: 'Meat & fish', priceGbp: 4.75, vatable: false },
  { sku: 'MEA-SAL-260', name: 'Scottish salmon fillets, 260g', dept: 'Meat & fish', priceGbp: 6.5, vatable: false },
  { sku: 'AMB-PST-500', name: 'Penne, 500g', dept: 'Ambient', priceGbp: 1.15, vatable: false, multibuy: '3for2' },
  { sku: 'AMB-COF-227', name: 'Ground coffee, 227g', dept: 'Ambient', priceGbp: 4.95, vatable: false },
  { sku: 'AMB-OIL-500', name: 'Extra virgin olive oil, 500ml', dept: 'Ambient', priceGbp: 5.4, vatable: false },
  { sku: 'HOU-DET-1L', name: 'Laundry liquid, 1L', dept: 'Household', priceGbp: 4.2, vatable: true },
  { sku: 'HOU-BAG-30', name: 'Compostable food bags, 30', dept: 'Household', priceGbp: 2.15, vatable: true },
];

export const CUSTOMERS = [
  { id: 'CUS-10482', name: 'Aoife Brennan', email: 'aoife.brennan@example.com', mobile: '+447700900112', postcode: 'EH8 9YL', tier: 'plus', since: '2024-03-11' },
  { id: 'CUS-10517', name: 'Rahul Menon', email: 'rahul.menon@example.com', mobile: '+447700900318', postcode: 'G12 8QQ', tier: 'standard', since: '2025-01-22' },
  { id: 'CUS-10623', name: 'Morag Stewart', email: 'morag.stewart@example.com', mobile: '+447700900447', postcode: 'KY16 9AJ', tier: 'plus', since: '2023-08-02' },
  { id: 'CUS-10744', name: 'Tomasz Wójcik', email: 'tomasz.wojcik@example.com', mobile: '+447700900561', postcode: 'FK8 1EA', tier: 'standard', since: '2025-06-30' },
  { id: 'CUS-10801', name: 'Yewande Adeyemi', email: 'yewande.adeyemi@example.com', mobile: '+447700900779', postcode: 'ML1 1TP', tier: 'plus', since: '2026-02-14' },
  // Deliberately outside the delivery area: the order-validator rejects this
  // one, so the console has a rejection to show and not just happy paths.
  { id: 'CUS-10999', name: 'Gareth Pugh', email: 'gareth.pugh@example.com', mobile: '+447700900884', postcode: 'CF10 1EP', tier: 'standard', since: '2026-08-19' },
];

export const DEPOTS = [
  { id: 'DEP-EDI', name: 'Edinburgh Newbridge', postcode: 'EH28 8PJ', vans: 18 },
  { id: 'DEP-GLA', name: 'Glasgow Cambuslang', postcode: 'G32 8RF', vans: 24 },
];

export type Order = ReturnType<typeof buildOrder>;

let seq = 0;

/** One order, shaped the way the checkout service emits it. */
export function buildOrder(customerIndex: number, picks: [string, number][], speed: 'standard' | 'express' = 'standard') {
  const customer = CUSTOMERS[customerIndex % CUSTOMERS.length];
  seq += 1;
  const lines = picks.map(([sku, quantity]) => {
    const p = PRODUCTS.find((x) => x.sku === sku)!;
    return {
      sku: p.sku,
      name: p.name,
      dept: p.dept,
      quantity,
      unitPriceGbp: p.priceGbp,
      vatable: p.vatable,
      ...(p.multibuy ? { multibuy: p.multibuy } : {}),
    };
  });
  return {
    orderId: `HRB-2026-${String(4800 + seq).padStart(5, '0')}`,
    placedAt: new Date().toISOString(),
    channel: seq % 3 === 0 ? 'ios' : seq % 3 === 1 ? 'web' : 'android',
    deliverySpeed: speed,
    customer: { id: customer.id, name: customer.name, email: customer.email, mobile: customer.mobile, tier: customer.tier },
    deliverTo: { postcode: customer.postcode, instructions: seq % 4 === 0 ? 'Leave with the neighbour at 12B' : '' },
    lines,
  };
}

/** A believable depot stock position, including one line that will run short. */
export function depotStock() {
  const stock: Record<string, { units: number; dailySales: number[] }> = {};
  for (const [i, p] of PRODUCTS.entries()) {
    // A predictable sawtooth rather than random, so two runs of the seed
    // produce the same screenshots.
    const base = 8 + ((i * 7) % 23);
    stock[p.sku] = {
      units: p.sku === 'BAK-SRD-800' ? 1 : base * 3,
      dailySales: Array.from({ length: 14 }, (_, d) => base + ((d * 3 + i) % 9) - 4),
    };
  }
  stock['BAK-CRS-4'] = { units: 60, dailySales: stock['BAK-CRS-4'].dailySales };
  return stock;
}

export const ORDER_BASKETS: [string, number][][] = [
  [['BAK-SRD-800', 1], ['DRY-OAT-1L', 3], ['PRD-SPN-200', 2], ['AMB-COF-227', 1]],
  [['MEA-SAL-260', 2], ['PRD-TOM-400', 1], ['AMB-OIL-500', 1], ['DRY-CHE-200', 1]],
  [['AMB-PST-500', 6], ['PRD-APP-6', 1], ['DRY-BTR-250', 2], ['HOU-DET-1L', 1]],
  [['BAK-CRS-4', 2], ['DRY-OAT-1L', 6], ['MEA-CHK-500', 1], ['HOU-BAG-30', 2]],
  [['PRD-SPN-200', 1], ['PRD-TOM-400', 2], ['BAK-SRD-800', 2], ['DRY-CHE-200', 1]],
];

/** The invoice a rendered receipt turns into, as plain text for S3. */
export function receiptText(order: any): string {
  const p = order.pricing ?? {};
  const money = (pence: number) => `£${(pence / 100).toFixed(2)}`;
  const rows = (p.lines ?? []).map(
    (l: any) => `  ${String(l.quantity).padStart(2)} × ${l.name.padEnd(36)} ${money(l.grossPence).padStart(8)}`
  );
  return [
    'HARBOUR GROCERIES',
    'Newbridge, Edinburgh EH28 8PJ · VAT GB 412 9981 22',
    ''.padEnd(62, '='),
    `Order ${order.orderId}`,
    `${order.customer?.name}  ·  ${order.deliverTo?.postcode}`,
    `Placed ${order.placedAt}`,
    ''.padEnd(62, '-'),
    ...rows,
    ''.padEnd(62, '-'),
    `  Subtotal${money(p.subtotalPence ?? 0).padStart(52)}`,
    `  Delivery${money(p.deliveryPence ?? 0).padStart(52)}`,
    `  VAT${money(p.vatPence ?? 0).padStart(57)}`,
    p.savedPence ? `  You saved${money(p.savedPence).padStart(50)}` : '',
    `  TOTAL${money(p.totalPence ?? 0).padStart(56)}`,
    '',
    'Thank you for shopping with Harbour.',
  ]
    .filter(Boolean)
    .join('\n');
}
