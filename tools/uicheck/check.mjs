// UI quality gates for DoesItOmarchy (PLAN.md §17.5), run against a live server:
//   - axe-core WCAG 2 A/AA on the key pages at 360px and 1440px
//   - colour contrast on a few pages in every theme (port map colours and the
//     known-limitation warning icon included)
//   - layout: no horizontal page scroll; matrix header links clickable
//
// Usage: BASE=http://127.0.0.1:8080 CHROME=/usr/bin/chromium node check.mjs
// Checks run in parallel, each in a fresh tab (JOBS, default: one per CPU).
// PAGES=/identify,/mac/ checks only the pages whose path starts with one of
// those prefixes, for a quick look while working on them.
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { availableParallelism } from "node:os";
import puppeteer from "puppeteer-core";

const require = createRequire(import.meta.url);
const axeSource = readFileSync(require.resolve("axe-core/axe.min.js"), "utf8");
const BASE = process.env.BASE || "http://127.0.0.1:8080";
const CHROME = process.env.CHROME || "/usr/bin/chromium";
const JOBS = Math.max(1, Number(process.env.JOBS) || availableParallelism());
const ONLY = (process.env.PAGES || "").split(",").map((s) => s.trim()).filter(Boolean);
const wanted = (path) => ONLY.length === 0 || ONLY.some((p) => path.startsWith(p));

const pages = [
  "/", "/search?q=mbp+2011", "/search?q=gpu%3A6770m", "/macs", "/mac/MacBookPro8-2", "/mac/MacBookPro8-2?view=matrix",
  "/mac/MacBookPro1-1", "/mac/MacBookPro15-1", "/mac/MacBookPro11-3", "/mac/MacPro5-1", "/mac/Macmini1-1", "/mac/Xserve3-1", "/criteria", "/stats", "/methodology",
  "/contribute", "/configs", "/components", "/changelog", "/attribution", "REPORT", "/mac/MacBookAir7-2", "/mac/MacBookAir5-2", "/mac/iMac10-1", "/mac/MacBook3-1", "/macs?q=gles%3A2.0", "/search?q=gles%3A2.0",
  "/identify", "/identify?product=MacBookPro8%2C2&pci=1002%3A6760", "/identify?product=MacBookPro8%2C2&pci=1002%3A6741", "/identify?none=1",
  "/identify?product=MacBookPro99%2C1", "/identify?product=MacBookPro8%2C2&pci=1002%3A6760&shared=1", "/identify?product=MacBookPro8%2C2&share=consent",
  "/api", "/api/mcp-cheatsheet", "/api/register", "REGISTER_STATUS", "/privacy", "/fixes", "/admin", "/admin/sources", "/admin/shares", "/admin/shares?all=1", "/admin/unsupported", "ADMIN_REPORT",
  "LIVE_REPORT", "ADMIN_LIVE_REPORT",
];
const widths = [360, 1440];
const contrastPages = ["/", "/api", "/mac/MacBookPro15-1", "/mac/MacBookPro11-3", "/identify?product=MacBookPro8%2C2&pci=1002%3A6760", "/admin/shares", "/admin/sources", "/mac/MacBook3-1", "/macs?q=gles%3A2.0"];

const failures = [];
const fail = (where, msg) => failures.push(`${where}: ${msg}`);

const browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ["--no-sandbox"] });

// open loads a path in a fresh tab at a width and, optionally, a theme.
async function open(path, width, theme) {
  // A browser context of its own: tabs in one context share localStorage,
  // so parallel theme checks overwrote each other's theme.
  const ctx = await browser.createBrowserContext();
  const page = await ctx.newPage();
  page.close = () => ctx.close();
  await page.setViewport({ width, height: 900 });
  await page.evaluateOnNewDocument((t) => {
    try { if (t) localStorage.setItem("theme", t); else localStorage.removeItem("theme"); } catch (e) {}
  }, theme || "");
  const res = await page.goto(BASE + path, { waitUntil: "networkidle0" });
  if (!res.ok()) throw new Error(`${path}: HTTP ${res.status()}`);
  if (!(await page.evaluate(() => typeof window.axe !== "undefined"))) await page.evaluate(axeSource);
  // Check what's folded away too: Identify my Mac's command explanations, and the
  // Hardware details with their port map drawings (fetched on open, PLAN §27).
  const drawings = await page.evaluate(() => {
    document.querySelectorAll("details.explain, details.hw").forEach((d) => { d.open = true; });
    return document.querySelectorAll("figure[data-pmsrc]").length;
  });
  if (drawings) await page.waitForNetworkIdle({ idleTime: 200 });
  return page;
}

async function axe(page, rules) {
  return page.evaluate(async (rules) => {
    const opts = rules ? { runOnly: { type: "rule", values: rules } } : { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] } };
    const r = await window.axe.run(document, opts);
    return r.violations.map((v) => `${v.id} (${v.impact}) ×${v.nodes.length}: ${v.help} [${v.nodes.slice(0, 2).map((n) => n.target.join(" ")).join(" | ")}]`);
  }, rules);
}

// pool runs the tasks, JOBS at a time.
async function pool(tasks) {
  let next = 0;
  const worker = async () => {
    while (next < tasks.length) {
      const task = tasks[next++];
      await task();
    }
  };
  await Promise.all(Array.from({ length: Math.min(JOBS, tasks.length) }, worker));
}

// A diagnostic report page: codes are random, so take the first one /stats
// links to. The theme list comes from the home page's picker.
const first = await open("/stats", 1440);
const report = await first.$eval('a[href^="/report/"]', (a) => a.getAttribute("href")).catch(() => null);
if (!report) fail("/stats", "no link to a diagnostic report (is the server running with -demo?)");
await first.goto(BASE + "/", { waitUntil: "networkidle0" });
const themes = await first.$$eval("#theme option", (os) => os.map((o) => o.value).filter((v) => v !== "system"));
// A source request's private status page (PLAN §30d) is reachable only by
// asking: submit the form once. It also puts a request on /admin/sources.
const regStatus = await first.evaluate(async () => {
  const body = new URLSearchParams({ name: "UI check", id: "ui-check-" + (Date.now() % 1e6), repo: "https://github.com/example/ui-check",
    email: "ui@example.com", description: "Checks the pages.", rules: "yes" });
  const r = await fetch("/api/register", { method: "POST", body });
  return r.ok ? new URL(r.url).pathname + new URL(r.url).search : null;
});
if (!regStatus) fail("/api/register", "submitting the form failed");
// The demo's live-boot reports: an accepted one on the iMac10,1, and a
// pending one in the review queue (the row with a tag).
await first.goto(BASE + "/mac/iMac10-1", { waitUntil: "networkidle0" });
const liveReport = await first.$eval('.results-list a[href^="/report/"]', (a) => a.getAttribute("href")).catch(() => null);
await first.goto(BASE + "/admin", { waitUntil: "networkidle0" });
const adminLive = await first.$eval('table.queue tr:has(.tag) a[href^="/admin/report/"]', (a) => a.getAttribute("href")).catch(() => null);
if (!liveReport || !adminLive) fail("/mac/iMac10-1", "no live-boot report (is the server running with -demo?)");
await first.close();
for (let i = 0; i < pages.length; i++) {
  if (pages[i] === "REGISTER_STATUS") pages[i] = regStatus || "/api/register";
  if (pages[i] === "REPORT") pages[i] = report || "/stats";
  if (pages[i] === "ADMIN_REPORT") pages[i] = report ? "/admin" + report : "/admin";
  if (pages[i] === "LIVE_REPORT") pages[i] = liveReport || "/stats";
  if (pages[i] === "ADMIN_LIVE_REPORT") pages[i] = adminLive || "/admin";
}
for (const list of [pages, contrastPages]) list.splice(0, list.length, ...list.filter(wanted));
if (pages.length + contrastPages.length === 0) {
  console.error(`uicheck: no page matches PAGES=${process.env.PAGES}`);
  await browser.close();
  process.exit(1);
}

const tasks = [];
// 1. Accessibility + layout at phone and desktop widths.
for (const path of pages) {
  for (const width of widths) {
    tasks.push(async () => {
      const where = `${path} @${width}`;
      const page = await open(path, width);
      for (const v of await axe(page)) fail(where, v);
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
      await page.close();
    });
  }
}
// 2. Colour contrast in every theme.
for (const theme of themes) {
  for (const path of contrastPages) {
    tasks.push(async () => {
      const page = await open(path, 1440, theme);
      const applied = await page.evaluate(() => document.documentElement.getAttribute("data-theme"));
      if (applied !== theme) fail(`${path} [${theme}]`, `theme not applied (got ${applied})`);
      for (const v of await axe(page, ["color-contrast"])) fail(`${path} [${theme}]`, v);
      // axe checks text only. Icons that carry meaning on their own (the
      // known-limitation warning, PLAN §30) need 3:1 against their
      // background (WCAG 1.4.11).
      const icons = await page.evaluate(() => {
        const rgb = (c) => (c.match(/[\d.]+/g) || []).map(Number);
        const lum = ([r, g, b]) => [r, g, b].map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; })
          .reduce((a, v, i) => a + v * [0.2126, 0.7152, 0.0722][i], 0);
        const bg = (el) => { for (; el; el = el.parentElement) { const c = rgb(getComputedStyle(el).backgroundColor); if (c.length === 3 || c[3] > 0) return c; } return [255, 255, 255]; };
        return [...document.querySelectorAll(".warn-i")].map((el) => {
          const a = lum(rgb(getComputedStyle(el).color)), b = lum(bg(el));
          return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
        });
      });
      if ((path === "/mac/MacBook3-1" || path.startsWith("/macs?q=gles")) && icons.length === 0) fail(`${path} [${theme}]`, "no warning icon found");
      for (const ratio of icons) if (ratio < 3) fail(`${path} [${theme}]`, `warning icon contrast ${ratio.toFixed(2)}:1, needs 3:1`);
      await page.close();
    });
  }
}

const started = Date.now();
await pool(tasks);
await browser.close();
const checks = pages.length * widths.length + themes.length * contrastPages.length;
const secs = ((Date.now() - started) / 1000).toFixed(0);
if (failures.length) {
  failures.sort();
  console.error(`uicheck: ${failures.length} problem(s) in ${checks} page checks\n` + failures.map((f) => "  " + f).join("\n"));
  process.exit(1);
}
console.log(`uicheck: ${checks} page checks passed in ${secs}s with ${JOBS} tabs (${pages.length} pages × ${widths.length} widths, ${themes.length} themes × ${contrastPages.length} pages)${ONLY.length ? ` · only PAGES=${ONLY.join(",")}` : ""}`);
