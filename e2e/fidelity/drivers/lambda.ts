import type { DriverTable } from './types';

// No board of this service is driven yet. Every board file in
// plan/design/boards for it therefore fails with "no driver for <board>":
// this file is that service stage's to-do list. Add one entry per board
// (see sqs.ts), or a `{ skip: reason }` where nothing can be driven.
export const drivers: DriverTable = {};
