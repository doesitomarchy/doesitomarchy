// Prints the MCP cheat sheet (PLAN §30e) to PDF, run by `make cheatsheet`:
//   - Letter and A4 must each come out as one page;
//   - writes OUT/mcp-cheatsheet.pdf (Letter) and OUT/mcp-cheatsheet.sum, the
//     SHA-256 of the page it printed, which TestCheatsheetPDF compares.
//
// Usage: BASE=http://127.0.0.1:18998 CHROME=/usr/bin/chromium OUT=../../internal/web node cheatsheet.mjs
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import puppeteer from "puppeteer-core";

const BASE = process.env.BASE || "http://127.0.0.1:8080";
const CHROME = process.env.CHROME || "/usr/bin/chromium";
const OUT = process.env.OUT || ".";
const URL = BASE + "/api/mcp-cheatsheet";

const res = await fetch(URL);
if (!res.ok) throw new Error(`${URL}: HTTP ${res.status}`);
const sum = createHash("sha256").update(Buffer.from(await res.arrayBuffer())).digest("hex");

const pages = (pdf) => (Buffer.from(pdf).toString("latin1").match(/\/Type\s*\/Page[^s]/g) || []).length;

const browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ["--no-sandbox"] });
let failed = false;
try {
  const page = await browser.newPage();
  await page.goto(URL, { waitUntil: "networkidle0" });
  await page.evaluate(() => document.fonts.ready);
  // How full the page is: the sheet's printed height against the page's
  // (Letter is the shorter page; the content is A4's width on both).
  await page.emulateMediaType("print");
  const used = await page.evaluate(() => document.querySelector(".sheet").getBoundingClientRect().height);
  const letter = await page.pdf({ preferCSSPageSize: true, printBackground: true });
  const a4 = await page.pdf({ format: "A4", printBackground: true });
  for (const [name, pdf, height] of [["Letter", letter, 11], ["A4", a4, 11.69]]) {
    const n = pages(pdf);
    const full = Math.round((100 * used) / ((height - 0.84) * 96));
    console.log(`cheatsheet: ${name}: ${n} page${n === 1 ? "" : "s"}, ${full}% full`);
    if (n !== 1) failed = true;
  }
  if (!failed) {
    writeFileSync(`${OUT}/mcp-cheatsheet.pdf`, letter);
    writeFileSync(`${OUT}/mcp-cheatsheet.sum`, sum + "\n");
    console.log(`cheatsheet: wrote ${OUT}/mcp-cheatsheet.pdf and .sum (${sum.slice(0, 12)})`);
  }
} finally {
  await browser.close();
}
if (failed) {
  console.error("cheatsheet: the sheet must fit one page on Letter and A4; nothing written");
  process.exit(1);
}
