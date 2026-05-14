export default {
  async fetch(request) {
    const url = new URL(request.url);
    url.hostname = "wod-strategist-api-dev-620752272029.asia-northeast3.run.app";
    url.port = "";

    const newRequest = new Request(url.toString(), {
      method: request.method,
      headers: request.headers,
      body: request.body,
      redirect: "follow",
    });

    return fetch(newRequest);
  },
};
