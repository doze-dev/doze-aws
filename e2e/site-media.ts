// site-media: the doze-dev.com pictures of the console, shot from a real
// doze-aws running Larder (doze-examples/larder), the made-up grocery shop the
// whole site is told through.
//
//   go tool task site-media                     (from the repo root)
//   bun site-media.ts [out-dir] [name...]       (from e2e/, binary already built)
//
// It boots the binary from a fresh directory named larder (so the console's
// header reads "doze-aws larder"), builds Larder's AWS with the aws CLI, sends
// it the traffic Larder's services would, and then shoots each page of the
// console (WebP stills) and the wire filling up (a WebM clip). Nothing on
// screen is drawn by this script: every resource, call, timing and log line is
// what the instance answered.
//
// What Larder declares in its doze.hcl: buckets product-images and receipts
// (versioned), and the pick-lists queue with a dead-letter queue after five
// receives. The rest is what such a shop would plausibly add, kept small: a
// Lambda that answers uploads to product-images, a DynamoDB table of
// deliveries, a fulfil-order state machine, a payment-settled rule that turns
// a paid order into a pick list, and the two roles those need.
//
// Env: SITE_MEDIA_HOLD=1 (keep the instance up after the shots), SITE_MEDIA_BIN (the doze-aws binary; default .tmp/site-media/doze-aws),
// SITE_MEDIA_PORT (default 14590), AWS_CLI (default the aws on PATH), and
// SITE_MEDIA_NODE (a node binary for the Lambda, when `node` on PATH is not
// one that runs).
import { chromium, type Page } from "@playwright/test";
import sharp from "sharp";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const [outArg, ...only] = process.argv.slice(2);
const OUT = path.resolve(outArg ?? "../../doze-website/public/media");
const BIN = path.resolve(process.env.SITE_MEDIA_BIN ?? ".tmp/site-media/doze-aws");
const PORT = Number(process.env.SITE_MEDIA_PORT ?? 14590);
const ENDPOINT = `http://127.0.0.1:${PORT}`;
const CONSOLE = `${ENDPOINT}/_console`;
const ACCOUNT = "000000000000";
const REGION = "us-east-1";
const W = 1440, H = 900, SCALE = 2, FPS = 30;
const want = (name: string) => !only.length || only.includes(name);

const AWS = process.env.AWS_CLI ?? Bun.which("aws");
if (!AWS) die("no aws CLI: put it on PATH or set AWS_CLI");
if (!fs.existsSync(BIN)) die(`no binary at ${BIN}: build it first (go tool task site-media does)`);
fs.mkdirSync(OUT, { recursive: true });

// ── boot ───────────────────────────────────────────────────────────────────

// A fresh directory named larder: the name is what the console's header shows,
// and an empty directory holds no doze-aws.toml for the binary to pick up.
const work = fs.mkdtempSync(path.join(os.tmpdir(), "doze-aws-site-media-"));
const home = path.join(work, "larder");
fs.mkdirSync(home);
let proc: ReturnType<typeof Bun.spawn>;

// ── Larder's AWS ───────────────────────────────────────────────────────────

const arn = {
  queue: (q: string) => `arn:aws:sqs:${REGION}:${ACCOUNT}:${q}`,
  fn: (f: string) => `arn:aws:lambda:${REGION}:${ACCOUNT}:function:${f}`,
  role: (r: string) => `arn:aws:iam::${ACCOUNT}:role/${r}`,
  table: (t: string) => `arn:aws:dynamodb:${REGION}:${ACCOUNT}:table/${t}`,
  machine: (m: string) => `arn:aws:states:${REGION}:${ACCOUNT}:stateMachine:${m}`,
};
const queueUrl = (q: string) => `${ENDPOINT}/${ACCOUNT}/${q}`;

// Products from Larder's catalog seed (services/catalog/seed), with the
// placeholder picture catalog draws for a product that has no photograph.
const PRODUCTS = [
  { sku: "FV-APL-4", name: "Braeburn apples", category: "fruit-veg" },
  { sku: "BK-SOU-1", name: "Sourdough loaf", category: "bakery" },
  { sku: "DY-EGG-6", name: "Free-range eggs", category: "dairy" },
  { sku: "FV-AVO-2", name: "Ripe & ready avocados", category: "fruit-veg" },
  { sku: "DY-CHD-1", name: "Mature cheddar", category: "dairy" },
  { sku: "BK-CRO-4", name: "All-butter croissants", category: "bakery" },
];
const SHELF: Record<string, string> = { "fruit-veg": "#7cb342", bakery: "#d4a056", dairy: "#90caf9" };

type Line = { sku: string; name: string; unit: string; qty: number; price_pence: number };
type Order = { id: string; email: string; card: string; slot: string; lines: Line[] };
const L = (sku: string, name: string, unit: string, qty: number, price_pence: number): Line => ({ sku, name, unit, qty, price_pence });
// Order ids are uuids, as Larder's orders service mints them. A card ending
// 0002 is declined (Larder's payments service says so).
const ORDERS: Order[] = [
  { id: "3f2b8c1e-5a4d-4e8f-9b21-7c6d0e1f2a31", email: "ada@example.com", card: "4242", slot: "18:00-19:00",
    lines: [L("FV-BAN-6", "Fairtrade bananas", "6 pack", 1, 95), L("DY-MLK-2", "Semi-skimmed milk", "2 pints", 2, 125), L("BK-SOU-1", "Sourdough loaf", "800g", 1, 325)] },
  { id: "8a7e4d2c-1b3f-4c6a-a5e9-2d8f7b1c0e44", email: "grace@example.com", card: "4444", slot: "18:00-19:00",
    lines: [L("DY-EGG-6", "Free-range eggs", "6 pack", 1, 210), L("BK-CRO-4", "All-butter croissants", "4 pack", 1, 250)] },
  { id: "c41d9e7a-6f2b-4a8c-b3d5-9e1a2f4c7b52", email: "linus@example.com", card: "0002", slot: "19:00-20:00",
    lines: [L("DY-CHD-1", "Mature cheddar", "400g", 1, 350), L("FV-APL-4", "Braeburn apples", "4 pack", 2, 160)] },
  { id: "5b6c2a9f-8d1e-4f3b-9c7a-4e2d1b8f6a63", email: "margaret@example.com", card: "1881", slot: "19:00-20:00",
    lines: [L("FV-AVO-2", "Ripe & ready avocados", "2 pack", 2, 199), L("FV-LEM-4", "Unwaxed lemons", "4 pack", 1, 130), L("DY-YOG-1", "Greek-style yoghurt", "500g", 1, 165)] },
  { id: "e9d3f1b7-2c5a-4d8e-8f6b-1a7c3e9d2b74", email: "ken@example.com", card: "4242", slot: "19:00-20:00",
    lines: [L("FV-POT-2K", "Maris Piper potatoes", "2kg", 1, 175), L("FV-CAR-1K", "Carrots", "1kg", 1, 70), L("DY-BUT-1", "Salted butter", "250g", 1, 225)] },
  // Traffic while the clip records.
  { id: "71a4c8e2-9b6d-4f1a-b2e8-6c3d9f5a1e85", email: "barbara@example.com", card: "5100", slot: "20:00-21:00",
    lines: [L("BK-CIN-2", "Cinnamon buns", "2 pack", 1, 275), L("DY-OAT-1", "Oat drink barista", "1 litre", 2, 200)] },
  { id: "a2f7d5c3-4e8b-4a9d-9e1c-8b5f2a6d3c96", email: "dennis@example.com", card: "4242", slot: "20:00-21:00",
    lines: [L("BK-BAG-5", "Plain bagels", "5 pack", 1, 140), L("DY-CHD-1", "Mature cheddar", "400g", 1, 350)] },
];

// The function catalog's uploads land on. It reads the event S3 sent and logs
// what arrived; turning the picture into thumbnails is not shown here.
const HANDLER = `exports.handler = async (event) => {
  const images = (event.Records || []).map((r) => ({ key: r.s3.object.key, bytes: r.s3.object.size }));
  for (const i of images) console.log("new product image", i.key, i.bytes + " bytes");
  return { images: images.length, keys: images.map((i) => i.key) };
};
`;

// fulfil-order: a paid order is announced (the payment-settled rule turns it
// into a pick list) and booked a delivery slot; a declined card fails the run.
const FULFIL_ORDER = {
  Comment: "A placed order: queue it for the warehouse and book its delivery, or stop on a declined card.",
  StartAt: "CheckPayment",
  States: {
    CheckPayment: {
      Type: "Choice",
      Choices: [{ Variable: "$.payment", StringEquals: "settled", Next: "AnnouncePayment" }],
      Default: "CardDeclined",
    },
    CardDeclined: { Type: "Fail", Error: "Payments.CardDeclined", Cause: "The card was declined; the order is not fulfilled." },
    AnnouncePayment: {
      Type: "Task",
      Resource: "arn:aws:states:::events:putEvents",
      Parameters: {
        Entries: [{ Source: "larder.payments", DetailType: "Payment Settled", "Detail.$": "$" }],
      },
      ResultPath: null,
      Next: "BookDelivery",
    },
    BookDelivery: {
      Type: "Task",
      Resource: "arn:aws:states:::dynamodb:putItem",
      Parameters: {
        TableName: "deliveries",
        Item: {
          order_id: { "S.$": "$.order_id" },
          slot: { "S.$": "$.slot" },
          status: { S: "awaiting-pick" },
        },
      },
      ResultPath: null,
      End: true,
    },
  },
};

const USERS: Record<string, Array<[string[], string]>> = {
  catalog: [[["s3:GetObject", "s3:PutObject"], "arn:aws:s3:::product-images/*"]],
  payments: [[["states:StartExecution"], arn.machine("fulfil-order")]],
  fulfilment: [
    [["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:ChangeMessageVisibility"], arn.queue("pick-lists")],
    [["s3:GetObject", "s3:PutObject"], "arn:aws:s3:::receipts/*"],
  ],
};

async function users() {
  for (const [name, statements] of Object.entries(USERS)) {
    await aws("iam", "create-user", "--user-name", name);
    await aws("iam", "put-user-policy", "--user-name", name, "--policy-name", name, "--policy-document", json(policy(...statements)));
    const k = JSON.parse(await aws("iam", "create-access-key", "--user-name", name)).AccessKey;
    keys[name] = { id: k.AccessKeyId, secret: k.SecretAccessKey };
  }
}

async function seed() {
  // The roles first: a function and a state machine are created with one.
  await aws("iam", "create-role", "--role-name", "resize-product-image",
    "--description", "Runs resize-product-image: reads uploads to product-images.",
    "--assume-role-policy-document", json(trust("lambda.amazonaws.com")));
  await aws("iam", "put-role-policy", "--role-name", "resize-product-image", "--policy-name", "product-images",
    "--policy-document", json(policy([["s3:GetObject", "s3:PutObject"], "arn:aws:s3:::product-images/*"])));
  await aws("iam", "create-role", "--role-name", "fulfil-order",
    "--description", "Runs the fulfil-order state machine.",
    "--assume-role-policy-document", json(trust("states.amazonaws.com")));
  await aws("iam", "put-role-policy", "--role-name", "fulfil-order", "--policy-name", "fulfil-order",
    "--policy-document", json(policy(
      [["events:PutEvents"], `arn:aws:events:${REGION}:${ACCOUNT}:event-bus/default`],
      [["dynamodb:PutItem"], arn.table("deliveries")],
    )));

  // What doze.hcl declares.
  await aws("s3api", "create-bucket", "--bucket", "product-images");
  await aws("s3api", "create-bucket", "--bucket", "receipts");
  await aws("s3api", "put-bucket-versioning", "--bucket", "receipts", "--versioning-configuration", "Status=Enabled");
  await aws("sqs", "create-queue", "--queue-name", "pick-lists-dlq");
  await aws("sqs", "create-queue", "--queue-name", "pick-lists", "--attributes", json({
    VisibilityTimeout: "30",
    RedrivePolicy: JSON.stringify({ deadLetterTargetArn: arn.queue("pick-lists-dlq"), maxReceiveCount: "5" }),
    Policy: JSON.stringify(policy([["sqs:SendMessage"], arn.queue("pick-lists")], undefined, { Service: "events.amazonaws.com" })),
  }));

  // What such a shop would add.
  await aws("dynamodb", "create-table", "--table-name", "deliveries",
    "--attribute-definitions", "AttributeName=order_id,AttributeType=S",
    "--key-schema", "AttributeName=order_id,KeyType=HASH", "--billing-mode", "PAY_PER_REQUEST");

  const zip = path.join(work, "resize-product-image.zip");
  fs.writeFileSync(path.join(work, "index.js"), HANDLER);
  await run(["zip", "-q", "-j", zip, path.join(work, "index.js")]);
  await aws("lambda", "create-function", "--function-name", "resize-product-image",
    "--runtime", "nodejs22.x", "--handler", "index.handler", "--role", arn.role("resize-product-image"),
    "--memory-size", "512", "--timeout", "10", "--zip-file", `fileb://${zip}`,
    "--description", "Answers each upload to product-images.");
  await aws("lambda", "add-permission", "--function-name", "resize-product-image", "--statement-id", "product-images",
    "--action", "lambda:InvokeFunction", "--principal", "s3.amazonaws.com", "--source-arn", "arn:aws:s3:::product-images");
  await aws("s3api", "put-bucket-notification-configuration", "--bucket", "product-images",
    "--notification-configuration", json({
      LambdaFunctionConfigurations: [{
        Id: "new-product-image", LambdaFunctionArn: arn.fn("resize-product-image"), Events: ["s3:ObjectCreated:*"],
        Filter: { Key: { FilterRules: [{ Name: "prefix", Value: "products/" }] } },
      }],
    }));

  await aws("events", "put-rule", "--name", "payment-settled",
    "--description", "A settled payment becomes a pick list for the warehouse.",
    "--event-pattern", json({ source: ["larder.payments"], "detail-type": ["Payment Settled"] }));
  await aws("events", "put-targets", "--rule", "payment-settled", "--targets", json([{
    Id: "pick-lists", Arn: arn.queue("pick-lists"),
    InputTransformer: {
      InputPathsMap: { order: "$.detail.order_id", lines: "$.detail.lines" },
      InputTemplate: '{"order_id": "<order>", "lines": <lines>}',
    },
  }]));

  await aws("stepfunctions", "create-state-machine", "--name", "fulfil-order",
    "--role-arn", arn.role("fulfil-order"), "--definition", json(FULFIL_ORDER));

  // Each service signs with its own access key, so IAM's access log can say
  // who asked for what. fulfilment's policy was written without
  // dynamodb:UpdateItem: soft mode serves the call and records the denial,
  // which is the gap the access log is there to show.
  await users();

  // The catalog's first pictures.
  for (const p of PRODUCTS.slice(0, 2)) await uploadImage(p);
}

// before is the shop's traffic before anyone is watching: five orders (one
// with a declined card) and two new product pictures.
async function before() {
  await traffic(ORDERS.slice(0, 1));
  await uploadImage(PRODUCTS[2]);
  await traffic(ORDERS.slice(1, 3));
  await uploadImage(PRODUCTS[3]);
  await traffic(ORDERS.slice(3, 5));
}

// after is what arrives while the clip watches, the refused call among it.
// beat is a pause between steps, so a viewer can follow the rows coming in.
async function after(pace = 0, beat: () => Promise<unknown> = async () => {}) {
  await uploadImage(PRODUCTS[4]);
  await beat();
  await traffic(ORDERS.slice(5, 6), pace);
  await refusedReceipt();
  await beat();
  await uploadImage(PRODUCTS[5]);
  await beat();
  await traffic(ORDERS.slice(6), pace);
}

// waiting is the last of the evening: three paid orders whose pick lists are
// still on the queue, and one the warehouse could not pick. It was received
// five times and never deleted, so the sixth receive moved it to
// pick-lists-dlq, as Larder's doze.hcl says it should.
async function waiting() {
  const late: Order[] = [
    { id: "0c9e2b6d-3a7f-4e1c-8d5b-f2a9c4e7b107", email: "frances@example.com", card: "4242", slot: "20:00-21:00",
      lines: [L("FV-TOM-V", "Vine cherry tomatoes", "250g", 2, 185), L("FV-SPN-B", "Baby spinach", "200g", 1, 150)] },
    { id: "6d1f8a3c-7e2b-4b9a-a4c6-3e8d1f5b2c18", email: "edsger@example.com", card: "4444", slot: "20:00-21:00",
      lines: [L("BK-WHL-1", "Wholemeal sandwich loaf", "800g", 1, 145), L("DY-MLK-2", "Semi-skimmed milk", "2 pints", 1, 125)] },
    { id: "b8a5e1f9-2d6c-4a3e-9f7b-5c1d8e2a4f29", email: "radia@example.com", card: "1881", slot: "21:00-22:00",
      lines: [L("FV-LEM-4", "Unwaxed lemons", "4 pack", 1, 130), L("DY-BUT-1", "Salted butter", "250g", 2, 225)] },
    { id: "d3c7a9e5-8b1f-4d2a-b6e4-9a2f7c1d5e30", email: "alan@example.com", card: "5100", slot: "21:00-22:00",
      lines: [L("FV-AVO-2", "Ripe & ready avocados", "2 pack", 1, 199)] },
  ];
  // The one that will not pick: its pick list goes first.
  await startExecution(late[3]);
  await Bun.sleep(500);
  for (let i = 0; i < 6; i++) {
    await awsAs("fulfilment", "sqs", "receive-message", "--queue-url", queueUrl("pick-lists"), "--visibility-timeout", "0");
  }
  for (const o of late.slice(0, 3)) await startExecution(o);
  await Bun.sleep(500);
}

async function startExecution(o: Order) {
  const settled = o.card !== "0002";
  await awsAs("payments", "stepfunctions", "start-execution", "--state-machine-arn", arn.machine("fulfil-order"),
    "--name", `order-${o.id.slice(0, 8)}`,
    "--input", json({ order_id: o.id, payment: settled ? "settled" : "declined", card_last4: o.card, slot: o.slot,
      lines: o.lines.map(({ sku, qty }) => ({ sku, qty })) }));
}

// traffic is the calls Larder's services make for each order: payments starts
// fulfil-order, the warehouse (fulfilment) takes the pick list off the queue,
// stores the receipt and marks the delivery out, and deletes the message.
async function traffic(orders: Order[], pace = 0) {
  const gap = () => (pace ? Bun.sleep(pace) : Promise.resolve());
  for (const o of orders) {
    await startExecution(o);
    await gap();
    if (o.card === "0002") continue;
    const msg = await receivePickList();
    await gap();
    await awsAs("fulfilment", "s3api", "put-object", "--bucket", "receipts", "--key", `receipts/${o.id}.txt`,
      "--content-type", "text/plain; charset=utf-8", "--body", file(`${o.id}.txt`, receipt(o)), "--if-none-match", "*");
    await gap();
    await awsAs("fulfilment", "dynamodb", "update-item", "--table-name", "deliveries", "--key", json({ order_id: { S: o.id } }),
      "--update-expression", "SET #s = :s", "--expression-attribute-names", json({ "#s": "status" }),
      "--expression-attribute-values", json({ ":s": { S: "out-for-delivery" } }));
    await gap();
    if (msg) await awsAs("fulfilment", "sqs", "delete-message", "--queue-url", queueUrl("pick-lists"), "--receipt-handle", msg);
    await gap();
  }
}

// receivePickList takes the next pick list off the queue, as the warehouse
// does (the state machine and the rule deliver one a moment after an
// execution starts).
async function receivePickList(): Promise<string | undefined> {
  for (let i = 0; i < 10; i++) {
    const out = await awsAs("fulfilment", "sqs", "receive-message", "--queue-url", queueUrl("pick-lists"), "--max-number-of-messages", "1");
    const m = out ? JSON.parse(out).Messages?.[0] : undefined;
    if (m) return m.ReceiptHandle;
    await Bun.sleep(300);
  }
  return undefined;
}

async function uploadImage(p: { sku: string; name: string; category: string }) {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 320 240"><rect width="320" height="240" fill="${SHELF[p.category]}"/>` +
    `<text x="160" y="128" font-family="system-ui, sans-serif" font-size="26" font-weight="600" fill="#fff" text-anchor="middle">${p.name.replace("&", "&amp;")}</text></svg>`;
  await awsAs("catalog", "s3api", "put-object", "--bucket", "product-images", "--key", `products/${p.sku}.svg`,
    "--content-type", "image/svg+xml", "--body", file(`${p.sku}.svg`, svg));
}

// receipt is Larder's plain-text receipt (services/fulfilment/receipt.py).
function receipt(o: Order) {
  const pounds = (p: number) => `£${Math.floor(p / 100)}.${String(p % 100).padStart(2, "0")}`;
  const total = o.lines.reduce((t, l) => t + l.qty * l.price_pence, 0);
  return [
    "LARDER — groceries at your door within the hour", "",
    `Order    ${o.id}`, `Customer ${o.email}`, "",
    ...o.lines.map((l) => `${String(l.qty).padStart(3)} x ${l.name.padEnd(28)} ${l.unit.padEnd(8)} ${pounds(l.qty * l.price_pence).padStart(8)}`),
    "", `${"Total".padEnd(43)} ${pounds(total).padStart(8)}`, `Paid by card ending ${o.card}`, "",
  ].join("\n");
}

// The refused call. The warehouse packs an order twice (a pick list that came
// back after its visibility timeout) and writes the receipt again, with
// If-None-Match: * so it can never overwrite one: S3 answers 412, as AWS does.
const REFUSED = ORDERS[1];
async function refusedReceipt() {
  await awsAs("fulfilment", "s3api", "put-object", "--bucket", "receipts", "--key", `receipts/${REFUSED.id}.txt`,
    "--content-type", "text/plain; charset=utf-8", "--body", file(`${REFUSED.id}.txt`, receipt(REFUSED)),
    "--if-none-match", "*").then(
    () => { throw new Error("the second receipt write was accepted: expected 412 PreconditionFailed"); },
    (e: Error) => { if (!e.message.includes("PreconditionFailed")) throw e; },
  );
}

// ── the clip ───────────────────────────────────────────────────────────────

async function live(browser: import("@playwright/test").Browser) {
  // The screencast is at CSS pixels whatever the device scale, so the page is
  // drawn at twice the size and zoomed by two: a 1440x900 window at Retina
  // sharpness (doze's test/desk-e2e/record.ts does the same).
  const page = await browser.newPage({ viewport: { width: W * SCALE, height: H * SCALE }, colorScheme: "dark" });
  await page.addInitScript((z) => addEventListener("DOMContentLoaded", () => (document.documentElement.style.zoom = String(z))), SCALE);
  await page.addInitScript(pointer);
  await page.goto(`${CONSOLE}/`);
  await settle(page);
  await page.mouse.move(W * SCALE * 0.62, H * SCALE * 0.6);
  at = { x: W * SCALE * 0.62, y: H * SCALE * 0.6 };
  await page.waitForTimeout(800);
  const file = path.join(OUT, "aws-live.webm");
  await record(page, file, async () => {
    await page.waitForTimeout(1200);
    // New calls arrive while the clip watches.
    await after(350, () => page.waitForTimeout(500));
    await page.waitForTimeout(2200);
    // Then open the one AWS would refuse.
    await clickZoomed(page, `.tr-row[data-refused="1"]`);
    await page.waitForTimeout(4500);
  });
  console.log(`${file} ${kb(file)}`);
  await page.close();
}

// ── the stills ─────────────────────────────────────────────────────────────

async function stills(browser: import("@playwright/test").Browser) {
  const ctx = await browser.newContext({ viewport: { width: W, height: H }, deviceScaleFactor: SCALE, colorScheme: "dark" });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => console.error("page error:", e.message));

  const shot = async (name: string, url: string, act?: () => Promise<void>) => {
    if (!want(name)) return;
    await page.goto(`${CONSOLE}${url}`);
    await settle(page);
    if (act) await act();
    await page.mouse.move(W - 4, H - 4);
    await page.waitForTimeout(400);
    const png = await page.screenshot();
    const file = path.join(OUT, `${name}.webp`);
    // One at a time: sharp under bun has hung on parallel work.
    fs.writeFileSync(file, await sharp(png).webp({ quality: 85 }).toBuffer());
    console.log(`${file} ${kb(file)}`);
  };

  await shot("aws-wire", "/");
  await shot("aws-call", "/", async () => {
    await page.locator(`.tr-row[data-refused="1"]`).first().click({ position: { x: 240, y: 12 } });
    await page.waitForTimeout(600);
  });
  await waiting();
  await shot("aws-map", "/map");
  await shot("aws-s3", "/s3/product-images", async () => {
    await page.getByText("products/", { exact: true }).first().click();
    await page.waitForTimeout(800);
  });
  await shot("aws-sqs", "/sqs/pick-lists");
  await shot("aws-dynamodb", "/ddb/deliveries");
  await shot("aws-lambda", "/lambda/resize-product-image", async () => {
    // Invoke it from its Test tab with the event S3 sent for the last upload.
    const p = PRODUCTS[5];
    const size = JSON.parse(await awsAs("catalog", "s3api", "head-object", "--bucket", "product-images", "--key", `products/${p.sku}.svg`)).ContentLength;
    const event = { Records: [{ eventSource: "aws:s3", eventName: "ObjectCreated:Put", awsRegion: REGION,
      s3: { bucket: { name: "product-images", arn: "arn:aws:s3:::product-images" }, object: { key: `products/${p.sku}.svg`, size } } }] };
    await setEditor(page, JSON.stringify(event, null, 2));
    await page.getByRole("button", { name: /^Invoke$/ }).first().click();
    await page.locator("#invoke-result .co-h").first().filter({ hasText: "succeeded" }).waitFor({ timeout: 20000 });
    await page.waitForTimeout(800);
  });
  await shot("aws-stepfunctions", "/sfn/fulfil-order");
  await shot("aws-eventbridge", "/eb/default/rule/payment-settled");
  await shot("aws-iam", "/iam", async () => {
    // The policy fulfilment's calls actually needed, generated from what soft
    // mode recorded, under the denial it found.
    await page.locator("select").first().selectOption({ label: "fulfilment" });
    await page.getByRole("button", { name: /Generate/ }).first().click();
    await page.waitForTimeout(1200);
    await page.evaluate(() => { document.querySelector("section.detail")!.scrollTop = 150; });
  });
  await ctx.close();
}

// setEditor fills the payload editor the way the e2e suite does.
async function setEditor(page: Page, text: string) {
  await page.evaluate((v) => (window as any).dozeEditor.set("textarea[name=payload][data-editor]", v), text);
}

// ── helpers ────────────────────────────────────────────────────────────────

async function settle(page: Page) {
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(500);
}

let at = { x: 0, y: 0 };
async function clickZoomed(page: Page, sel: string) {
  const box = await page.locator(sel).first().boundingBox();
  if (!box) throw new Error(`nothing at ${sel}`);
  const x = box.x + Math.min(box.width / 2, 260), y = box.y + box.height / 2;
  await glide(page, x, y);
  await page.waitForTimeout(250);
  await page.mouse.click(x, y);
}

// glide moves the pointer there over about half a second, eased.
async function glide(page: Page, x: number, y: number) {
  const from = { ...at };
  const n = 24;
  for (let i = 1; i <= n; i++) {
    const t = i / n;
    const e = t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2;
    await page.mouse.move(from.x + (x - from.x) * e, from.y + (y - from.y) * e);
    await page.waitForTimeout(16);
  }
  at = { x, y };
}

// record screencasts the page while act runs, then encodes what it saw at a
// steady rate: a frame comes only when the page changes, so each is held until
// the next.
async function record(page: Page, file: string, act: () => Promise<void>) {
  const cdp = await page.context().newCDPSession(page);
  const frames: { t: number; jpg: Buffer }[] = [];
  cdp.on("Page.screencastFrame", async (f: { data: string; sessionId: number; metadata: { timestamp: number } }) => {
    frames.push({ t: f.metadata.timestamp, jpg: Buffer.from(f.data, "base64") });
    await cdp.send("Page.screencastFrameAck", { sessionId: f.sessionId }).catch(() => {});
  });
  const vp = page.viewportSize()!;
  await cdp.send("Page.startScreencast", { format: "jpeg", quality: 92, maxWidth: vp.width, maxHeight: vp.height, everyNthFrame: 1 });
  const start = Date.now() / 1000;
  await page.waitForTimeout(300);
  await act();
  const end = Date.now() / 1000;
  await cdp.send("Page.stopScreencast");
  if (!frames.length) throw new Error("no frames");
  // Playwright's ffmpeg reads no pipes: the frames go through a file.
  const tmp = `${file}.mjpeg`;
  const out = fs.openSync(tmp, "w");
  let i = 0;
  for (let t = start; t < end; t += 1 / FPS) {
    while (i + 1 < frames.length && frames[i + 1].t <= t) i++;
    fs.writeSync(out, frames[i].jpg);
  }
  fs.closeSync(out);
  const code = await run([findFFmpeg(), "-loglevel", "error", "-y", "-f", "image2pipe", "-framerate", String(FPS), "-c:v", "mjpeg", "-i", tmp,
    "-c:v", "libvpx-vp9", "-pix_fmt", "yuv420p", "-crf", "34", "-b:v", "0", "-deadline", "good", "-cpu-used", "2", "-row-mt", "1", "-an", file]);
  fs.rmSync(tmp, { force: true });
  if (code !== 0) throw new Error("ffmpeg failed");
}

// pointer draws a macOS-like arrow where the mouse is, so a viewer of the clip
// sees what was clicked.
function pointer() {
  addEventListener("DOMContentLoaded", () => {
    const p = document.createElement("div");
    p.innerHTML = '<svg width="22" height="28" viewBox="0 0 22 28"><path d="M2 2 L2 22 L7.5 17 L11.5 26 L15 24.5 L11 15.5 L18.5 15.5 Z" fill="#111" stroke="#fff" stroke-width="1.6" stroke-linejoin="round"/></svg>';
    Object.assign(p.style, { position: "fixed", left: "0", top: "0", zIndex: "2147483647", pointerEvents: "none", transform: "translate(-2px,-2px)" });
    document.body.appendChild(p);
    addEventListener("mousemove", (e) => { const z = Number(document.documentElement.style.zoom || 1); p.style.left = e.clientX / z + "px"; p.style.top = e.clientY / z + "px"; }, true);
  });
}

// aws runs one aws CLI command against the instance and returns its stdout.
// A failure throws, with what the CLI said. as names the Larder service whose
// access key signs it (see users); without one it is the account's own.
const aws = (...args: string[]) => awsAs(undefined, ...args);
const keys: Record<string, { id: string; secret: string }> = {};
async function awsAs(as: string | undefined, ...args: string[]): Promise<string> {
  const k = as ? keys[as] : undefined;
  const p = Bun.spawn([AWS!, "--endpoint-url", ENDPOINT, "--region", REGION, "--output", "json", ...args], {
    env: k ? { ...awsEnv, AWS_ACCESS_KEY_ID: k.id, AWS_SECRET_ACCESS_KEY: k.secret } : awsEnv,
    stdout: "pipe", stderr: "pipe",
  });
  const [out, err, code] = await Promise.all([new Response(p.stdout).text(), new Response(p.stderr).text(), p.exited]);
  if (code !== 0) throw new Error(`aws ${args.slice(0, 2).join(" ")}: ${err.trim()}`);
  return out;
}

// The credentials Larder's services use; doze-aws takes any. No profile, and
// no config file, so nothing on this machine changes what the CLI sends.
const awsEnv = Object.fromEntries(Object.entries({ ...process.env,
  AWS_ACCESS_KEY_ID: "larder", AWS_SECRET_ACCESS_KEY: "larder", AWS_CONFIG_FILE: "/dev/null",
  AWS_SHARED_CREDENTIALS_FILE: "/dev/null", AWS_PAGER: "" })
  .filter(([k]) => !["AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_SESSION_TOKEN", "AWS_ENDPOINT_URL"].includes(k)));

async function run(cmd: string[]) {
  const p = Bun.spawn(cmd, { stdout: "inherit", stderr: "inherit" });
  return p.exited;
}

async function waitUp() {
  for (let i = 0; i < 100; i++) {
    const ok = await fetch(`${CONSOLE}/`).then((r) => r.ok, () => false);
    if (ok) return;
    if (proc.exitCode !== null) break;
    await Bun.sleep(100);
  }
  throw new Error(`doze-aws did not come up on ${ENDPOINT}`);
}

function trust(service: string) {
  return { Version: "2012-10-17", Statement: [{ Effect: "Allow", Principal: { Service: service }, Action: "sts:AssumeRole" }] };
}
function policy(...rest: Array<[string[], string] | undefined | Record<string, string>>) {
  const pairs = rest.filter((r): r is [string[], string] => Array.isArray(r));
  const principal = rest.find((r) => r && !Array.isArray(r)) as Record<string, string> | undefined;
  return {
    Version: "2012-10-17",
    Statement: pairs.map(([Action, Resource]) => ({ Effect: "Allow", ...(principal ? { Principal: principal } : {}), Action, Resource })),
  };
}
const json = (v: unknown) => JSON.stringify(v);
function file(name: string, body: string) {
  const f = path.join(work, name);
  fs.writeFileSync(f, body);
  return f;
}
const kb = (f: string) => `${Math.round(fs.statSync(f).size / 1024)} KB`;

// findNode is the directory of a node that runs, for the Lambda's nodejs22.x
// runtime: SITE_MEDIA_NODE, the node on PATH, or the newest one proto keeps.
function findNode(): string | undefined {
  const runs = (bin: string) => Bun.spawnSync([bin, "--version"], { stdout: "ignore", stderr: "ignore" }).exitCode === 0;
  if (process.env.SITE_MEDIA_NODE) return path.dirname(process.env.SITE_MEDIA_NODE);
  const onPath = Bun.which("node");
  if (onPath && runs(onPath)) return undefined;
  const proto = path.join(os.homedir(), ".proto/tools/node");
  if (fs.existsSync(proto)) {
    const vs = fs.readdirSync(proto).filter((v) => /^\d/.test(v)).sort((a, b) => b.localeCompare(a, undefined, { numeric: true }));
    for (const v of vs) if (runs(path.join(proto, v, "bin/node"))) return path.join(proto, v, "bin");
  }
  die("no node for the Lambda's nodejs22.x runtime: put one on PATH or set SITE_MEDIA_NODE");
}

// findFFmpeg is Playwright's own ffmpeg (it has VP9), or one on the PATH.
function findFFmpeg() {
  const cache = path.join(os.homedir(), os.platform() === "darwin" ? "Library/Caches/ms-playwright" : ".cache/ms-playwright");
  const dirs = fs.existsSync(cache) ? fs.readdirSync(cache).filter((d) => d.startsWith("ffmpeg-")).sort().reverse() : [];
  for (const d of dirs) for (const f of ["ffmpeg-mac", "ffmpeg-linux", "ffmpeg"]) {
    const p = path.join(cache, d, f);
    if (fs.existsSync(p)) return p;
  }
  return "ffmpeg";
}

// ── run ──────────────────────────────────────────────────────────────────────

const nodeDir = findNode();
proc = Bun.spawn(
  [BIN, "--listen", `127.0.0.1:${PORT}`, "--data-dir", "data", "--console", "--name", "larder", "--region", REGION],
  {
    cwd: home,
    env: { ...process.env, PATH: nodeDir ? `${nodeDir}:${process.env.PATH}` : process.env.PATH! },
    stdout: Bun.file(path.join(work, "doze-aws.log")),
    stderr: Bun.file(path.join(work, "doze-aws.err")),
  },
);
// Ctrl-C (or a hold ended) stops the instance too, not just this script.
for (const sig of ["SIGINT", "SIGTERM"] as const) process.on(sig, () => { proc.kill(); process.exit(130); });
let exitCode = 0;
try {
  await waitUp();
  await seed();
  await before();

  const browser = await chromium.launch();
  try {
    if (want("aws-live")) await live(browser);
    else await after();
    await stills(browser);
    // SITE_MEDIA_HOLD=1 keeps the instance up after the shots, to look around.
    if (process.env.SITE_MEDIA_HOLD) {
      console.log(`holding: ${CONSOLE}/ (Ctrl-C to stop)`);
      await new Promise(() => {});
    }
  } finally {
    await browser.close();
  }
} catch (e) {
  console.error(e);
  console.error(`doze-aws log: ${work}/doze-aws.err`);
  exitCode = 1;
} finally {
  proc.kill();
  await proc.exited;
  if (!exitCode) fs.rmSync(work, { recursive: true, force: true });
}
process.exit(exitCode);

function die(msg: string): never {
  console.error(`site-media: ${msg}`);
  process.exit(2);
}
