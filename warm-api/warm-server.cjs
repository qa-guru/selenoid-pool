#!/usr/bin/env node
"use strict";

const http = require("http");
const { URL } = require("url");
const { VideoRecorder } = require("./video-recorder.cjs");

function env(name, defaultValue = "") {
  const value = process.env[name];
  return typeof value === "string" && value.length > 0 ? value : defaultValue;
}

function parsePort(name, defaultValue) {
  const port = Number(env(name, String(defaultValue)));
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`${name} must be a valid port`);
  }
  return port;
}

function readJsonBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    req.on("data", (chunk) => chunks.push(chunk));
    req.on("end", () => {
      if (chunks.length === 0) {
        resolve({});
        return;
      }
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString("utf8")));
      } catch (error) {
        reject(new Error("Invalid JSON body"));
      }
    });
    req.on("error", reject);
  });
}

function sendJson(res, statusCode, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(statusCode, {
    "Content-Type": "application/json; charset=utf-8",
    "Content-Length": Buffer.byteLength(body),
  });
  res.end(body);
}

/**
 * @param {object} options
 * @param {string} options.protocol - "webdriver" | "playwright"
 * @param {() => Promise<object>} options.getStatus
 * @param {(url: string) => Promise<object>} options.goto
 * @param {() => Promise<object>} options.reset
 */
function createWarmServer(options) {
  const slotId = env("WARM_SLOT_ID", env("HOSTNAME", "slot"));
  const sessionId = env("WARM_SESSION_ID", slotId);
  const port = parsePort("WARM_PORT", 8080);
  const videoDir = env("WARM_VIDEO_DIR", "/data/video");
  const display = env("DISPLAY", ":99");
  const videoSize = env("VIDEO_SIZE", "1920x1080");
  const frameRate = Number(env("VIDEO_FRAME_RATE", "12")) || 12;

  const recorder = new VideoRecorder({
    videoDir,
    display,
    videoSize,
    frameRate,
    sessionId,
  });

  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url || "/", `http://${req.headers.host || "localhost"}`);
    const path = url.pathname.replace(/\/+$/, "") || "/";

    try {
      if (req.method === "GET" && path === "/warm/status") {
        const status = await options.getStatus();
        sendJson(res, 200, {
          ready: true,
          protocol: options.protocol,
          slotId,
          sessionId,
          video: recorder.status(),
          ...status,
        });
        return;
      }

      if (req.method === "POST" && path === "/warm/goto") {
        const body = await readJsonBody(req);
        if (!body.url || typeof body.url !== "string") {
          sendJson(res, 400, { error: "url is required" });
          return;
        }
        const result = await options.goto(body.url);
        sendJson(res, 200, { ok: true, url: body.url, ...result });
        return;
      }

      if (req.method === "POST" && path === "/warm/reset") {
        const result = await options.reset();
        sendJson(res, 200, { ok: true, ...result });
        return;
      }

      if (req.method === "POST" && path === "/warm/video/start") {
        const body = await readJsonBody(req);
        const result = await recorder.start(body.sessionId || sessionId);
        sendJson(res, 200, { ok: true, ...result });
        return;
      }

      if (req.method === "POST" && path === "/warm/video/stop") {
        const result = await recorder.stop();
        sendJson(res, 200, { ok: true, ...result });
        return;
      }

      if (req.method === "GET" && path === "/") {
        sendJson(res, 200, {
          service: "qaguru-warm-api",
          protocol: options.protocol,
          slotId,
          sessionId,
        });
        return;
      }

      sendJson(res, 404, { error: "not found" });
    } catch (error) {
      sendJson(res, 500, {
        error: error instanceof Error ? error.message : String(error),
      });
    }
  });

  return {
    slotId,
    sessionId,
    port,
    recorder,
    start() {
      return new Promise((resolve, reject) => {
        server.listen(port, "0.0.0.0", () => {
          console.log(
            `[warm-api] listening on :${port} protocol=${options.protocol} slot=${slotId} session=${sessionId}`,
          );
          resolve(server);
        });
        server.on("error", reject);
      });
    },
    async stop() {
      await recorder.stop().catch(() => {});
      await new Promise((resolve) => server.close(resolve));
    },
  };
}

module.exports = { createWarmServer, env, parsePort };
