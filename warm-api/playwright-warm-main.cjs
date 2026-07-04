#!/usr/bin/env node
"use strict";

const { chromium, firefox, webkit } = require("playwright-core");
const { createWarmServer, env } = require("./warm-server.cjs");

const browserTypes = { chromium, firefox, webkit };

async function main() {
  const browserTypeName = env("PW_BROWSER_TYPE", "chromium");
  const browserType = browserTypes[browserTypeName];
  if (!browserType) {
    throw new Error(`Unsupported PW_BROWSER_TYPE=${browserTypeName}`);
  }

  const wsEndpoint = env("PW_WS_ENDPOINT");
  if (!wsEndpoint) {
    throw new Error("PW_WS_ENDPOINT is required for playwright warm handlers");
  }

  let browser = null;
  let page = null;

  async function ensurePage() {
    if (!browser) {
      browser = await browserType.connect(wsEndpoint);
    }
    const contexts = browser.contexts();
    if (contexts.length > 0 && contexts[0].pages().length > 0) {
      page = contexts[0].pages()[0];
      return page;
    }
    const context = contexts[0] || (await browser.newContext());
    page = context.pages()[0] || (await context.newPage());
    return page;
  }

  const warm = createWarmServer({
    protocol: "playwright",
    async getStatus() {
      return {
        playwrightWsEndpoint: wsEndpoint,
        currentUrl: page ? page.url() : null,
      };
    },
    async goto(url) {
      const activePage = await ensurePage();
      await activePage.goto(url, { waitUntil: "domcontentloaded" });
      return { currentUrl: activePage.url() };
    },
    async reset() {
      const activePage = await ensurePage();
      await activePage.context().clearCookies();
      await activePage.goto("about:blank", { waitUntil: "domcontentloaded" });
      return { currentUrl: activePage.url() };
    },
  });

  await warm.start();

  const shutdown = async () => {
    await warm.stop();
    if (browser) {
      await browser.close().catch(() => {});
    }
    process.exit(0);
  };

  process.on("SIGINT", shutdown);
  process.on("SIGTERM", shutdown);
}

main().catch((error) => {
  console.error("[playwright-warm] fatal:", error);
  process.exit(1);
});
