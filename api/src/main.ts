import { createServer } from "node:http";
import { WebSocketServer, WebSocket } from "ws";

const PORT = Number(process.env.PORT ?? 3000);
const HOST = process.env.HOST ?? "0.0.0.0";

const server = createServer((_req, res) => {
  res.writeHead(200, { "content-type": "text/plain" });
  res.end("Hello World!\n");
});

const sourceWss = new WebSocketServer({ noServer: true });
const destinationWss = new WebSocketServer({ noServer: true });

let source: WebSocket | null = null;
const destinations = new Set<WebSocket>();

sourceWss.on("connection", (ws) => {
  if (source) {
    ws.close(1013, "source already connected");
    return;
  }

  source = ws;
  console.log("source connected");

  ws.on("message", (data, isBinary) => {
    if (!isBinary) {
      return;
    }

    for (const dest of destinations) {
      if (dest.readyState === WebSocket.OPEN) {
        dest.send(data, { binary: true });
      }
    }
  });

  ws.on("close", () => {
    if (source === ws) {
      source = null;
    }

    console.log("source disconnected");
  });

  ws.on("error", (err) => {
    console.error("source error", err);
  });
});

destinationWss.on("connection", (ws) => {
  destinations.add(ws);
  console.log(`destination connected (total=${destinations.size})`);

  ws.on("close", () => {
    destinations.delete(ws);
    console.log(`destination disconnected (total=${destinations.size})`);
  });

  ws.on("error", (err) => {
    console.error("destination error", err);
  });
});

server.on("upgrade", (req, socket, head) => {
  const { url } = req;

  if (url === "/source") {
    sourceWss.handleUpgrade(req, socket, head, (ws) => {
      sourceWss.emit("connection", ws, req);
    });
  } else if (url === "/destination") {
    destinationWss.handleUpgrade(req, socket, head, (ws) => {
      destinationWss.emit("connection", ws, req);
    });
  } else {
    socket.destroy();
  }
});

server.listen(PORT, HOST, () => {
  console.log(`sonic-bridge api listening on ${HOST}:${PORT}`);
});
