package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

func main() {
	payload := map[string]any{
		"type":          "email",
		"to":            "user@example.com",
		"subject":       "Welcome",
		"template_body": "Hi {{name}}, your OTP is {{otp}}.",
		"data": map[string]any{
			"name": "Siddharth",
			"otp":  "481902",
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:8080/v1/notify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "dev-secret-key")
	req.Header.Set("Idempotency-Key", "go-example-1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	var response map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&response)
	fmt.Println(resp.StatusCode, response)
}
