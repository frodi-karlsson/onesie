# Jev client

The `jev` package is a Go client for the TypeSafe System One API and compatible providers.
The onesie CLI uses the same client.

```go
client, err := jev.New(jev.WithAPIKey(apiKey))
if err != nil {
	return err
}

result, err := client.SystemOne(ctx, jev.Request{
	State: "Please restore service today.",
	Questions: jev.Questions{
		{ID: "urgent", Question: jev.Noul{Instructions: "Is this urgent?"}},
		{ID: "team", Question: jev.Choice{
			Instructions: "Which team should handle this?",
			Criteria: jev.Criteria{
				{Name: "billing", Desc: "Payments, invoicing, refunds"},
				{Name: "technical", Desc: "Bugs, outages, integrations"},
				{Name: "other"},
			},
		}},
	},
})
if err != nil {
	return err
}

urgent, err := result.Noul("urgent")
if err != nil {
	return err
}

fmt.Println(urgent.Noul)
```

The client uses the TypeSafe provider by default and reads `TYPESAFE_API_KEY`. Pass
`jev.WithAPIKey` for explicit credentials, `jev.WithProvider` to select OpenRouter or Berget,
and `jev.WithHTTPClient` to supply a transport. Requests accept a context so the calling service
can set its own deadline and cancellation policy.

Use the CLI for streams, question files, shell assertions, caching and calibration. Those workflows
remain in the onesie command.
