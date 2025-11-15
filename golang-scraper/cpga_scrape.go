package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
)

func maincpga() {
	fmt.Println("🔐 Starting CPGA Login Test...")

	// Create HTTP client with cookie jar to maintain session
	jar, err := cookiejar.New(nil)
	if err != nil {
		fmt.Println("Error creating cookie jar:", err)
		return
	}
	client := &http.Client{
		Jar: jar, // This will automatically handle session cookies
	}

	// Step 1: Perform Login
	fmt.Println("\n📝 Step 1: Attempting login...")
	token, loginSuccess := performLogin(client)

	if !loginSuccess {
		fmt.Println("❌ Login failed. Stopping...")
		return
	}

	fmt.Println("✅ Login successful! Now testing authenticated requests...")

	// Step 2: Test authenticated GET requests
	fmt.Println("\n🧪 Step 2: Testing authenticated access...")
	testAuthenticatedRequests(client, token)
}

func performLogin(client *http.Client) (string, bool) {
	// Using the correct endpoint from network inspection
	loginURL := "https://cpga.onrender.com/api/user/login"
	fmt.Printf("\n🔍 Using endpoint: %s\n", loginURL)

	jsonData := []byte(`{"emailOrUsername":"1shJ", "password":"supsiddyp?"}`)

	req, err := http.NewRequest("POST", loginURL, bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		return "", false
	}

	// Set headers matching the network inspection
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://cpga.vercel.app")
	req.Header.Set("Referer", "https://cpga.vercel.app/login")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36")

	// Execute request
	res, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error making request: %v\n", err)
		return "", false
	}
	defer res.Body.Close()

	// Read response
	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		return "", false
	}

	fmt.Printf("📊 Status: %s\n", res.Status)
	fmt.Printf("📄 Response: %s\n", string(body))

	// Check for success indicators
	if res.StatusCode == 200 || res.StatusCode == 201 {
		var loginResponse map[string]interface{}
		if err := json.Unmarshal(body, &loginResponse); err == nil {
			if status, exists := loginResponse["status"]; exists {
				if status == "success" || status == "ok" {
					if token, hasToken := loginResponse["token"]; hasToken {
						tokenStr := fmt.Sprintf("%v", token)
						fmt.Printf("🔑 JWT Token extracted: %s...\n", tokenStr[:50])
						fmt.Println("✅ Login successful!")
						return tokenStr, true
					}
					fmt.Println("✅ Login successful!")
					return "", true
				}
			}
			if token, hasToken := loginResponse["token"]; hasToken {
				tokenStr := fmt.Sprintf("%v", token)
				fmt.Printf("🔑 JWT Token extracted: %s...\n", tokenStr[:50])
				fmt.Println("✅ Login successful (token received)!")
				return tokenStr, true
			}
			if _, hasUser := loginResponse["user"]; hasUser {
				fmt.Println("✅ Login successful (user data received)!")
				return "", true
			}
		}

		if res.StatusCode == 200 {
			fmt.Println("✅ Login likely successful (HTTP 200)!")
			return "", true
		}
	}

	// Check for authentication errors
	if res.StatusCode == 400 || res.StatusCode == 401 || res.StatusCode == 422 {
		fmt.Printf("🔍 Authentication failed: %d\n", res.StatusCode)
		fmt.Printf("Response: %s\n", string(body))
		return "", false
	}

	fmt.Printf("❌ Unexpected response: %d\n", res.StatusCode)
	return "", false
}

func testAuthenticatedRequests(client *http.Client, token string) {
	// Test 1: Dashboard/Profile page
	fmt.Println("\n🔍 Test 1: Accessing profile...")
	testGetRequest(client, "https://cpga.onrender.com/api/user/profile", "Profile", token)

	// Test 2: User data endpoint
	fmt.Println("\n🔍 Test 2: Accessing user data...")
	testGetRequest(client, "https://cpga.onrender.com/api/user/me", "User Data", token)

	// Test 3: Protected dashboard content
	fmt.Println("\n🔍 Test 3: Accessing dashboard...")
	testGetRequest(client, "https://cpga.vercel.app/dashboard", "Dashboard", "")

	// Test 4: API user info
	fmt.Println("\n🔍 Test 4: Accessing user info...")
	testGetRequest(client, "https://cpga.onrender.com/api/user", "User Info", token)
}

func testGetRequest(client *http.Client, url, description, token string) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Printf("❌ Error creating %s request: %v\n", description, err)
		return
	}

	// Add JWT token to Authorization header if provided
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		fmt.Printf("🔑 Using JWT token for %s\n", description)
	}

	// Add headers to look like a real browser request
	req.Header.Set("Accept", "application/json, text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://cpga.vercel.app/dashboard")

	res, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ Error accessing %s: %v\n", description, err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Printf("❌ Error reading %s response: %v\n", description, err)
		return
	}

	// Analyze response
	fmt.Printf("📊 %s Response: %s\n", description, res.Status)

	if res.StatusCode == 200 {
		fmt.Printf("✅ %s: Authenticated access successful!\n", description)
		fmt.Printf("📏 Content length: %d characters\n", len(string(body)))

		// Show first 200 characters of response
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			fmt.Printf("📖 Preview: %s...\n", bodyStr[:200])
		} else {
			fmt.Printf("📖 Content: %s\n", bodyStr)
		}
	} else if res.StatusCode == 401 || res.StatusCode == 403 {
		fmt.Printf("⚠️ %s: Authentication required (Status: %d)\n", description, res.StatusCode)
	} else if res.StatusCode == 404 {
		fmt.Printf("📭 %s: Endpoint not found (might not exist)\n", description)
	} else {
		fmt.Printf("🔍 %s: Unexpected response (Status: %d)\n", description, res.StatusCode)
		fmt.Printf("📖 Response: %s\n", string(body))
	}
}
