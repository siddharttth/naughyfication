const payload = {
  type: "email",
  to: "user@example.com",
  subject: "Welcome",
  template_body: "Hi {{name}}, your OTP is {{otp}}.",
  data: {
    name: "Siddharth",
    otp: "481902"
  }
};

fetch("http://127.0.0.1:8080/v1/notify", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-API-Key": "dev-secret-key",
    "Idempotency-Key": "node-example-1"
  },
  body: JSON.stringify(payload)
})
  .then(async (response) => {
    const body = await response.json();
    console.log(response.status, body);
  })
  .catch((error) => {
    console.error("request failed", error);
  });
