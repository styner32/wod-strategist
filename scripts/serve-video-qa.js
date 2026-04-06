const fs = require("node:fs");
const http = require("node:http");
const path = require("node:path");

const PORT = Number(process.env.VIDEO_QA_PORT || 3000);
const HOST = process.env.VIDEO_QA_HOST || "0.0.0.0";
const repoRoot = path.resolve(__dirname, "..");

const routes = {
  "/": "video.html",
  "/video.html": "video.html",
  "/video.css": "video.css",
  "/video.js": "video.js",
};

const contentTypes = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
};

function send(response, statusCode, body, contentType = "text/plain; charset=utf-8") {
  response.writeHead(statusCode, {
    "Content-Type": contentType,
    "Cache-Control": "no-store",
  });
  response.end(body);
}

const server = http.createServer((request, response) => {
  const pathname = new URL(request.url, `http://${request.headers.host || "localhost"}`).pathname;
  const fileName = routes[pathname];

  if (!fileName) {
    send(response, 404, "Not found");
    return;
  }

  const filePath = path.join(repoRoot, fileName);
  try {
    const data = fs.readFileSync(filePath);
    const ext = path.extname(fileName);
    send(response, 200, data, contentTypes[ext] || "application/octet-stream");
  } catch (error) {
    send(response, 500, `Failed to read ${fileName}: ${error.message}`);
  }
});

server.listen(PORT, HOST, () => {
  console.log(`Video QA server listening on http://localhost:${PORT}`);
  console.log(`Also reachable via http://127.0.0.1:${PORT}`);
});
