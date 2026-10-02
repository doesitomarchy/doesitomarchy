// UI quality gates for DoesItOmarchy (PLAN.md §17.5), run against a live server:
//   - axe-core WCAG 2 A/AA on the key pages at 360px and 1440px
//   - colour contrast on two pages in every theme
//   - layout: no horizontal page scroll; matrix header links clickable
//
// Usage: BASE=http://127.0.0.1:8080 CHROME=/usr/bin/chromium node check.mjs
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import puppeteer from "puppeteer-core";

const require = createRequire(import.meta.url);
const axeSource = readFileSync(require.resolve("axe-core/axe.min.js"), "utf8");
const BASE = process.env.BASE || "http://127.0.0.1:8080";
const CHROME = process.env.CHROME || "/usr/bin/chromium";

const pages = [
  "/", "/search?q=mbp+2011", "/search?q=gpu%3A6770m", "/macs", "/mac/MacBookPro8-2", "/mac/MacBookPro8-2?view=matrix",
  "/mac/MacBookPro1-1", "/mac/MacBookPro15-1", "/mac/Xserve3-1", "/criteria", "/stats", "/methodology",
  "/contribute", "/configs", "/components", "/changelog", "/attribution", "REPORT", "/mac/MacBookAir7-2",
  "/identify", "/identify?product=MacBookPro8%2C2&pci=1002%3A6760", "/identify?product=MacBookPro8%2C2&pci=1002%3A6741", "/identify?none=1",
  "/identify?product=MacBookPro99%2C1", "/identify?product=MacBookPro8%2C2&pci=1002%3A6760&shared=1", "/identify?product=MacBookPro8%2C2&share=consent",
  "/api", "/privacy", "/admin", "/admin/sources", "/admin/shares", "/admin/shares?all=1", "ADMIN_REPORT",
];
const widths = [360, 1440];
const contrastPages = ["/", "/mac/MacBookPro15-1", "/identify?product=MacBookPro8%2C2&pci=1002%3A6760", "/admin/shares"];

const failures = [];
const fail = (where, msg) => failures.push(`${where}: ${msg}`);

const browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ["--no-sandbox"] });
const page = await browser.newPage();

async function load(path, width, theme) {
  await page.setViewport({ width, height: 900 });
  await page.evaluateOnNewDocument((t) => {
    try { if (t) localStorage.setItem("theme", t); else localStorage.removeItem("theme"); } catch (e) {}
  }, theme || "");
  const res = await page.goto(BASE + path, { waitUntil: "networkidle0" });
  if (!res.ok()) throw new Error(`${path}: HTTP ${res.status()}`);
  if (!(await page.evaluate(() => typeof window.axe !== "undefined"))) await page.evaluate(axeSource);
  // Check what's folded away too: Identify my Mac's command explanations.
  await page.evaluate(() => document.querySelectorAll("details.explain").forEach((d) => { d.open = true; }));
}

async function axe(rules) {
  return page.evaluate(async (rules) => {
    const opts = rules ? { runOnly: { type: "rule", values: rules } } : { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] } };
    const r = await window.axe.run(document, opts);
    return r.violations.map((v) => `${v.id} (${v.impact}) ×${v.nodes.length}: ${v.help} [${v.nodes.slice(0, 2).map((n) => n.target.join(" ")).join(" | ")}]`);
  }, rules);
}

// A diagnostic report page: codes are random, so take the first one /stats links to.
await load("/stats", 1440);
const report = await page.$eval('a[href^="/report/"]', (a) => a.getAttribute("href")).catch(() => null);
if (!report) fail("/stats", "no link to a diagnostic report (is the server running with -demo?)");
for (let i = 0; i < pages.length; i++) {
  if (pages[i] === "REPORT") pages[i] = report || "/stats";
  if (pages[i] === "ADMIN_REPORT") pages[i] = report ? "/admin" + report : "/admin";
}

// 1. Accessibility + layout at phone and desktop widths.
for (const path of pages) {
  for (const width of widths) {
    const where = `${path} @${width}`;
    await load(path, width);
    for (const v of await axe()) fail(where, v);
    const layout = await page.evaluate(() => {
      const out = [];
      const de = document.documentElement;
      if (de.scrollWidth > de.clientWidth + 1) out.push(`horizontal scroll: ${de.scrollWidth}px > ${de.clientWidth}px`);
      document.querySelectorAll(".matrix th.col a").forEach((a, i) => {
        if (i > 40) return; // a sample is enough on the big matrix page
        a.scrollIntoView({ block: "center", inline: "center" });
        const r = a.getBoundingClientRect();
        const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        if (!hit || !(hit === a || a.contains(hit))) out.push(`matrix header link not clickable: ${a.textContent.trim()}`);
      });
      return out;
    });
    for (const l of layout) fail(where, l);
  }
}

// 2. Colour contrast in every theme.
await load("/", 1440);
const themes = await page.$$eval("#theme option", (os) => os.map((o) => o.value).filter((v) => v !== "system"));
for (const theme of themes) {
  for (const path of contrastPages) {
    await load(path, 1440, theme);
    const applied = await page.evaluate(() => document.documentElement.getAttribute("data-theme"));
    if (applied !== theme) fail(`${path} [${theme}]`, `theme not applied (got ${applied})`);
    for (const v of await axe(["color-contrast"])) fail(`${path} [${theme}]`, v);
  }
}

await browser.close();
const checks = pages.length * widths.length + themes.length * contrastPages.length;
if (failures.length) {
  console.error(`uicheck: ${failures.length} problem(s) in ${checks} page checks\n` + failures.map((f) => "  " + f).join("\n"));
  process.exit(1);
}
console.log(`uicheck: ${checks} page checks passed (${pages.length} pages × ${widths.length} widths, ${themes.length} themes × ${contrastPages.length} pages)`);
