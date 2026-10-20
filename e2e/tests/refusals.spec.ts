import { test, expect } from '../fixtures/console';
import { createQueue } from '../fixtures/api';

// What a refusal and a failed page load look like in the browser, for the pieces
// the foundation (package P6) changed: a refusal the form used to hide, a form
// that sent a request nobody meant, and the page for a thing that is not there.

test.describe('Refusals and failed pages', () => {
  test('a stream of zero shards is refused where the form is, and not made', async ({ page, uniqueName }) => {
    const stream = uniqueName('e2e-zero');
    await page.goto('kinesis/create');
    await page.locator('input[name="name"]').fill(stream);
    await page.locator('input[name="shards"]').fill('0');
    await page.getByRole('button', { name: 'Create stream' }).click();
    const err = page.locator('.err');
    await expect(err).toContainText('ValidationException');
    await expect(err).toContainText('shardCount');
    await expect(page).toHaveURL(/kinesis\/create/);
    const gone = await page.request.get(`kinesis/${stream}`, { headers: { accept: 'text/html' } });
    expect(gone.status()).toBeGreaterThanOrEqual(400);
  });

  test('an empty bucket name is refused by the form in its own words', async ({ page }) => {
    await page.goto('s3/create');
    await page.getByRole('button', { name: 'Create bucket' }).click();
    const err = page.locator('.err');
    await expect(err).toContainText('a bucket needs a name');
    await expect(err).not.toContainText('MethodNotAllowed');
  });

  test('a missing queue is a page in the shell with the queue list beside it', async ({ page, request, uniqueName }) => {
    const real = uniqueName('e2e-real');
    await createQueue(request, real);
    await page.goto(`sqs/${uniqueName('e2e-missing')}`);
    await expect(page.getByRole('heading', { name: 'Not found' })).toBeVisible();
    await expect(page.locator('.listpane')).toBeVisible();
    await expect(page.locator('.listpane').getByText(real)).toBeVisible();
    await expect(page.getByRole('link', { name: /Back to SQS/ })).toBeVisible();
  });
});
