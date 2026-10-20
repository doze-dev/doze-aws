import type { Ctx, DriverTable } from './types';

// KMS's boards are driven by the KMS stage; its to-do list is every board file
// in plan/design/boards for it that is not here. Kms-19-Fidelity is the
// shell's fidelity dialog (foundation D-c) drawn for KMS, so it is driven with
// the dialog.

/** A KMS JSON 1.1 call, unsigned, as the harbour fixture's own calls are. */
async function kms(c: Ctx, op: string, body: Record<string, unknown>) {
  const r = await fetch(`${c.server.origin}/`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-amz-json-1.1', 'X-Amz-Target': `TrentService.${op}` },
    body: JSON.stringify(body),
  });
  const j: any = await r.json();
  if (!r.ok) throw new Error(`${op} -> ${r.status} ${JSON.stringify(j).slice(0, 200)}`);
  return j;
}

export const drivers: DriverTable = {
  'Kms-19-Fidelity': {
    async drive(c) {
      // The board's open key, harbour-checkout, enabled.
      const k = await kms(c, 'CreateKey', { Description: 'harbour checkout' });
      await kms(c, 'CreateAlias', { AliasName: 'alias/harbour-checkout', TargetKeyId: k.KeyMetadata.KeyId });
      await c.page.goto(c.url(`kms/${k.KeyMetadata.KeyId}`));
      await c.page.locator('.info-btn').first().click();
      await c.page.locator('.fid-card').first().waitFor();
      await c.settle(400);
    },
    note: 'The board draws the dialog as 1360 wide with "What is real" as a right-hand column and the title "Fidelity"; the page draws the shell\'s Fidelity board (940, "KMS · what is real here", the differences under the table). Two boards draw one dialog two ways; the plan names the shell\'s (D-c).',
  },
};
