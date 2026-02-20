/**
 * Claim Base Sepolia ETH up to the daily limit via Coinbase CDP Faucets API.
 *
 * Rate limit: 1000 claims/day × 0.0001 ETH = max 0.1 ETH/day
 *
 * Usage:
 *   cp .env.example .env  # fill in CDP_API_KEY_ID, CDP_API_KEY_SECRET, EOA_ADDRESS
 *   npm install
 *   npm run claim
 *   npm run claim -- --claims 100   # specify number of claims (default: 1000)
 */

import { CdpClient } from "@coinbase/cdp-sdk";
import "dotenv/config";

const DAILY_LIMIT = 1000;
const ETH_PER_CLAIM = 0.0001;
const NETWORK = "base-sepolia";
const DELAY_MS = 200; // avoid hammering the API

function parseArgs(): { claims: number } {
  const args = process.argv.slice(2);
  const idx = args.indexOf("--claims");
  if (idx !== -1 && args[idx + 1]) {
    const n = parseInt(args[idx + 1], 10);
    if (isNaN(n) || n < 1 || n > DAILY_LIMIT) {
      console.error(`--claims must be between 1 and ${DAILY_LIMIT}`);
      process.exit(1);
    }
    return { claims: n };
  }
  return { claims: DAILY_LIMIT };
}

async function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function main(): Promise<void> {
  const address = process.env.EOA_ADDRESS;
  if (!address) {
    console.error("EOA_ADDRESS is not set. Copy .env.example to .env and fill it in.");
    process.exit(1);
  }

  if (!process.env.CDP_API_KEY_ID || !process.env.CDP_API_KEY_SECRET) {
    console.error("CDP_API_KEY_ID and CDP_API_KEY_SECRET must be set.");
    process.exit(1);
  }

  const { claims } = parseArgs();
  const expectedEth = (claims * ETH_PER_CLAIM).toFixed(4);

  console.log(`Target address : ${address}`);
  console.log(`Network        : ${NETWORK}`);
  console.log(`Claims         : ${claims} (expected ~${expectedEth} ETH)`);
  console.log("");

  const cdp = new CdpClient();

  let succeeded = 0;
  let failed = 0;

  for (let i = 1; i <= claims; i++) {
    try {
      const res = await cdp.evm.requestFaucet({
        address,
        network: NETWORK,
        token: "eth",
      });
      succeeded++;
      console.log(
        `[${i}/${claims}] ✓ https://sepolia.basescan.org/tx/${res.transactionHash}`
      );
    } catch (err: unknown) {
      failed++;
      const msg = err instanceof Error ? err.message : String(err);
      console.error(`[${i}/${claims}] ✗ ${msg}`);

      // Stop immediately if daily limit is exhausted
      if (msg.includes("rate limit") || msg.includes("limit exceeded")) {
        console.error("Daily limit reached. Stopping.");
        break;
      }
    }

    if (i < claims) {
      await sleep(DELAY_MS);
    }
  }

  const totalEth = (succeeded * ETH_PER_CLAIM).toFixed(4);
  console.log("");
  console.log(`Done. succeeded=${succeeded}, failed=${failed}, total≈${totalEth} ETH`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
