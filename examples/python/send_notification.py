import requests

payload = {
    "type": "email",
    "to": "user@example.com",
    "subject": "Welcome",
    "template_body": "Hi {{name}}, your OTP is {{otp}}.",
    "data": {
        "name": "Siddharth",
        "otp": "481902",
    },
}

response = requests.post(
    "http://127.0.0.1:8080/v1/notify",
    json=payload,
    headers={
        "X-API-Key": "dev-secret-key",
        "Idempotency-Key": "python-example-1",
    },
    timeout=10,
)

print(response.status_code, response.json())
