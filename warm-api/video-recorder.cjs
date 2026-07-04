"use strict";

const { spawn } = require("child_process");
const fs = require("fs");
const path = require("path");

function timestampSuffix() {
  return String(Date.now());
}

function buildFileName(sessionId, suffix) {
  const safeSession = String(sessionId).replace(/[^a-zA-Z0-9._-]+/g, "_");
  return `${safeSession}-${suffix}.mp4`;
}

class VideoRecorder {
  /**
   * @param {object} options
   * @param {string} options.videoDir
   * @param {string} options.display
   * @param {string} options.videoSize
   * @param {number} options.frameRate
   * @param {string} options.sessionId
   */
  constructor(options) {
    this.videoDir = options.videoDir;
    this.display = options.display;
    this.videoSize = options.videoSize;
    this.frameRate = options.frameRate;
    this.defaultSessionId = options.sessionId;
    this.process = null;
    this.currentFile = null;
    this.currentSessionId = null;
    fs.mkdirSync(this.videoDir, { recursive: true });
  }

  status() {
    return {
      recording: this.process !== null,
      file: this.currentFile,
      sessionId: this.currentSessionId,
    };
  }

  async start(sessionId = this.defaultSessionId) {
    if (this.process) {
      throw new Error("video recording is already running");
    }

    const fileName = buildFileName(sessionId, timestampSuffix());
    const outputPath = path.join(this.videoDir, fileName);
    const [width, height] = this.videoSize.split("x");

    const args = [
      "-y",
      "-f",
      "x11grab",
      "-video_size",
      `${width}x${height}`,
      "-framerate",
      String(this.frameRate),
      "-i",
      this.display,
      "-codec:v",
      "libx264",
      "-preset",
      "ultrafast",
      "-pix_fmt",
      "yuv420p",
      outputPath,
    ];

    this.process = spawn("ffmpeg", args, {
      stdio: ["ignore", "pipe", "pipe"],
    });
    this.currentFile = fileName;
    this.currentSessionId = sessionId;

    this.process.on("exit", () => {
      this.process = null;
    });

    this.process.stderr.on("data", (chunk) => {
      const line = chunk.toString().trim();
      if (line.includes("Error") || line.includes("error")) {
        console.error(`[warm-video] ${line}`);
      }
    });

    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => resolve(), 400);
      this.process.once("error", (error) => {
        clearTimeout(timer);
        this.process = null;
        reject(error);
      });
    });

    console.log(`[warm-video] started ${fileName} session=${sessionId}`);
    return {
      sessionId,
      file: fileName,
      path: outputPath,
    };
  }

  async stop() {
    if (!this.process) {
      return { recording: false, file: null };
    }

    const file = this.currentFile;
    const sessionId = this.currentSessionId;
    const proc = this.process;

    await new Promise((resolve) => {
      const timer = setTimeout(() => {
        try {
          proc.kill("SIGKILL");
        } catch (_) {
          /* ignore */
        }
        resolve();
      }, 5000);

      proc.once("exit", () => {
        clearTimeout(timer);
        resolve();
      });

      try {
        proc.kill("SIGINT");
      } catch (_) {
        clearTimeout(timer);
        resolve();
      }
    });

    this.process = null;
    this.currentFile = null;
    this.currentSessionId = null;

    console.log(`[warm-video] stopped ${file}`);
    return {
      recording: false,
      sessionId,
      file,
      path: file ? path.join(this.videoDir, file) : null,
    };
  }
}

module.exports = { VideoRecorder, buildFileName, timestampSuffix };
