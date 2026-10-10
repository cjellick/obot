---
title: Filters
---

## Overview

Filters are a powerful mechanism for inspecting and controlling tool calls and their results in the MCP Gateway. They provide administrators with the ability to implement custom validation, logging, security checks, or other business logic by intercepting tool requests and responses before they are processed.

Filters can be implemented in three ways:

- **Guardrail filters**: Checks that Obot runs itself, with [guardrail](./guardrails.md) detectors such as secrets detection, PII detection, an LLM judge or an external content safety service. They block, redact or log what the detectors find. No server, container or deployment is involved.
- **MCP filter servers**: Hosted or remote MCP servers that expose a filter tool. Hosted filters run as separate Docker containers or Kubernetes workloads. Remote filters run at an external MCP endpoint. Obot calls the configured tool when matching MCP traffic is processed. Obot's [built-in filters](#built-in-filters) are MCP filter servers.
- **HTTP webhook filters**: HTTP endpoints that receive MCP messages and return a decision to allow or reject them.

When you configure a filter, you can narrow when it runs using selectors that target particular tool calls or MCP (Model Context Protocol) tool functions.

Guardrail filters run inside the Obot process. Other filter implementations run outside it: Obot handles proxying, calls the filter, and enforces its response. In both cases the target MCP server does not host or enforce the filter. See the [gateway architecture](../concepts/mcp-gateway.md#gateway-architecture).

## How Filters Work

1. **MCP Request Interception**: When a request is made to an MCP server, the gateway intercepts it and sends the details to your configured filter
2. **Payload Inspection**: The filter server or webhook receives the payload and performs its validation. A guardrail filter is checked by Obot itself.
3. **Response Decision**: Your filter returns one of the following decisions:
   - Accept: Allow the tool call to proceed
   - Reject: Block execution and return an error to the user
   - Mutate: Return a modified MCP message, if mutation is allowed for the filter
4. **Enforcement**: Obot applies the filter response before forwarding the request or returning the response to the client.

When several filters apply to a server, they run as a chain, ordered by the filter's ID. Each filter sees the message as the filters before it left it, so a mutation by one filter is what the next one checks. A rejection by any filter blocks the message. Responses pass through the chain in the same order.

Filters don't apply to Obot's own system MCP servers, such as filter servers themselves, or to the component servers of a composite server, whose traffic is filtered on the composite server.

Each filter that runs on a message is recorded with the message in the MCP audit log, under the filter's name, with its outcome (`ok`, `mutated` or `rejected`) and reason. For a filter server, the audit log also names the filter tool that was called. For a guardrail filter, it says `guardrail`.

## Gateway Configuration

Filters are configured under **MCP Servers > Filters**. Use **Add New Filter** to create a guardrail filter, a custom filter (an HTTP webhook or an MCP server), or one of the built-in filters. See below for the configuration setup for each type. The list of filters shows each filter's type.

### Selectors

All filter types support selectors to control when your filter is triggered:

- **Specific MCP Tool Call Methods**: Target particular tools or functions
- **MCP Tool Names, URIS**: Choose which MCP servers the filter applies to

## Guardrail Filters

A guardrail filter checks MCP traffic with [guardrail detectors](./guardrails.md#detectors), the same detectors that check LLM gateway traffic. Obot runs it in-process, in the filter chain with any other filters, so there is no filter server to deploy, configure, launch or restart, and no server details, logs or usage to show. Its detail page has its configuration and its audit logs.

To create one, select **Add New Filter > Create Guardrail** under **MCP Servers > Filters**, then configure:

- **Name**: A descriptive name. It names the filter in block messages, the MCP audit log and guardrail events.
- **MCP servers and selectors**: Which servers and which methods, tools, prompts or resources the filter applies to, as for any filter.
- **Detectors**: One or more guardrail detectors. Use **Manage detectors** to create or test them.
- **Action**:
  - **Block** rejects the message with an error naming the filter and what was found. You can replace that message with your own.
  - **Redact** replaces what was found with a label such as `[REDACTED:EMAIL]` and lets the rest through. Some detectors can only tell that content is a problem, not where it is. A redacting filter blocks when one of those finds something.
  - **Log** only records an event.
- **Direction**: Whether to check requests to the MCP server, its responses, or both.
- **Fail closed**: Whether to block when a detector or the condition can't be evaluated, for example because an external service is down or a detector was deleted. By default, the filter is skipped and an error event is recorded. If Obot can't load guardrail detectors at all, for example because its database is unreachable, guardrail filters block regardless of this setting, as any filter that errors does.
- **Sampling rate**: The percentage of matching messages to check, for expensive detectors.
- **Assigned to**: Users, groups or everyone. A filter with no assignment applies to everyone.
- **Condition** (optional): A [CEL](https://github.com/google/cel-spec) expression that must be true for the filter to apply. It can use `mcp_method`, `mcp_tool` (the tool or prompt name, or the resource URI), `mcp_arguments` (the tool call's or prompt's arguments), `mcp_server` (the server's display name), `headers` (the request's headers, without credentials), and `user.id` and `user.groups`. For example: `mcp_tool.startsWith("create_") && !("admins" in user.groups)`. Reading a header or argument that isn't there is an error, so test for it first: `"path" in mcp_arguments`.

### What a guardrail filter checks

| Method | Request | Response |
| --- | --- | --- |
| `tools/call` | Every string in the tool's arguments | The result's text content, embedded text resources and structured content |
| `resources/read` | Nothing | The text contents of the resource |
| `prompts/get` | Every string in the prompt's arguments | The text of the prompt's messages, including embedded text resources |

Images and other binary content are not checked. Other methods carry no content to check and pass through a guardrail filter unchanged.

A blocked request never reaches the MCP server, and a blocked response is replaced by a JSON-RPC error. Responses are checked whichever stream they arrive on, including a GET stream when a client resumes a stream or uses the legacy SSE transport. While any filter applies to a server, JSON-RPC batches sent to it are refused, since filters check one message at a time. A guardrail filter blocks a request whose parameters it can't read.

Every finding and every evaluation error is recorded as a guardrail event, under the filter's name, with the MCP server and tool, prompt or resource. MCP and LLM events are listed together in the guardrail events. The MCP audit log records both the original and the redacted message.

A guardrail filter is shown as not configured while one of its detectors is missing or can't be used, for example because it lacks a required credential. It still runs, and fails as described under **Fail closed**.

## MCP Filter Servers

MCP filters can be deployed as MCP servers. Their deployment configuration is similar to other MCP servers in Obot: choose a runtime such as `remote`, `containerized`, `npx`, or `uvx`, then provide the runtime-specific configuration and any required environment variables.

With `remote`, Obot connects to the configured endpoint and does not deploy the filter implementation. The other runtimes deploy a separate workload; a hosted filter does not run inside Obot or inside the target MCP server.

The additional requirement for an MCP filter server is a filter tool name. Obot needs this value so it knows which tool to call when the filter runs.

### Filter Tool Contract

When you write an MCP server that acts as a filter, implement the tool called by `filterConfig.toolName` with this contract:

1. Obot calls one tool on the MCP server for filtering.
2. The tool accepts the full MCP message as an argument.
3. The tool returns a response with these fields:

| Field | Description |
|-------|-------------|
| `accept` | Whether the MCP message should be allowed to continue. |
| `mutated` | Whether the filter returned a modified MCP message. Use this only when the filter is allowed to mutate traffic. |
| `message` | The MCP message to use after filtering. For non-mutating filters, return the original message. |
| `reason` | Human-readable explanation for the decision. Include this when rejecting or mutating a message so administrators can understand the result. |

See the [obot-platform/pii-filter](https://github.com/obot-platform/pii-filter) repository for an example MCP filter server.

## Built-in Filters

Obot ships with a default set of built-in filters. These are MCP filter servers that are already configured for deployment in Obot through the system MCP catalog.

The default built-in filter catalog is maintained in the [obot-platform/system-mcp-catalog](https://github.com/obot-platform/system-mcp-catalog) repository.

## HTTP-based Filters

Deploy a webhook service that accepts MCP messages over HTTP. Configure its URL in Obot, which sends matching messages to the webhook and uses its response to allow or reject them.

Provide the following information to Obot:

### Required Configuration

- **Name**: A descriptive name for your filter
- **URL**: The webhook endpoint that receives MCP messages
- **Secret** (optional): A shared secret with the webhook receiver for payload signature verification

### Security with Secrets

Configure the same shared secret in Obot and your webhook service. When a secret is configured, webhook requests include a signature in the `X-Obot-Signature-256` header. Your service should verify this signature before processing the request.

### Webhook Receiver

Your webhook endpoint must accept HTTP POST requests containing MCP messages.

### Payload Structure

The webhook will receive a payload with this data structure:

```python
# Pydantic Model
class WebhookMessage(BaseModel):
    """JSON-RPC message structure for webhook payloads."""
    jsonrpc: str
    id: Optional[Union[str, int]] = None
    method: Optional[str] = None
    params: Optional[Dict[str, Any]] = None
    result: Optional[Dict[str, Any]] = None
    error: Optional[Dict[str, Any]] = None
```

- **Signature Header**: Used for verifying the payload authenticity (when secrets are configured)

`X-Obot-Signature-256`

### Response Codes

Your webhook service should respond with:

- **HTTP 200**: Accept the tool call - allows execution to proceed
- **Non-200 codes**: Reject the tool call - blocks execution and may return an error to the user

### Example

This Python example inspects the search tools query param in the DuckDuckGo MCP Server.

To run this example, execute the following once you have saved the script in `simple_webhook_example.py`:

```bash
uv add fastapi uvicorn pydantic
PORT=8000 WEBHOOK_SECRET=somethingsecret uv run simple_webhook_example.py
```

The filter target url will be `http://<host>:8000/webhook`

Set the Webhook Secret in Obot to the same value as `WEBHOOK_SECRET` in this example.

```python
#!/usr/bin/env python3
"""
Simple webhook endpoint with signature validation

Usage:
    uv run simple_webhook_example.py

Environment Variables:
    WEBHOOK_SECRET: Secret for webhook signature validation (default: "test_secret")
    PORT: Port to run the server on (default: 8000)
"""

import hmac
import hashlib
import os
import logging
from typing import Dict, Any, List, Optional, Union
from fastapi import FastAPI, Request, HTTPException, Header
from pydantic import BaseModel

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)

def validate_signature(body: bytes, signature: str, secret: str) -> bool:
    """
    Validate HMAC-SHA256 signature for webhook security.
    
    Args:
        body: Raw request payload
        signature: Signature from X-Obot-Signature-256 header
        secret: Shared secret for validation
    
    Returns:
        True if signature is valid, False otherwise
    """
    # Remove "sha256=" prefix if present
    if signature.startswith("sha256="):
        signature = signature[7:]
    
    # Calculate expected signature
    expected = hmac.new(
        secret.encode('utf-8'), 
        body, 
        hashlib.sha256
    ).hexdigest()
    
    # Secure comparison
    return hmac.compare_digest(signature, expected)

class WebhookMessage(BaseModel):
    """JSON-RPC message structure for webhook payloads."""
    jsonrpc: str
    id: Optional[Union[str, int]] = None
    method: Optional[str] = None
    params: Optional[Dict[str, Any]] = None
    result: Optional[Dict[str, Any]] = None
    error: Optional[Dict[str, Any]] = None

# Initialize app
app = FastAPI(title="Simple Webhook with Filtering")

# Configuration
SECRET = os.getenv("WEBHOOK_SECRET", "test_secret")
PORT = int(os.getenv("PORT", "8000"))

@app.post("/webhook")
async def webhook_endpoint(
    request: Request,
    x_obot_signature_256: str = Header(alias="X-Obot-Signature-256")
):
    """
    Main webhook endpoint that validates signatures and processes messages.
    """
    body = await request.body()
    
    # Log the incoming request
    logger.info(f"📥 Webhook called - Method: {request.method}, URL: {request.url}")
    logger.info(f"📄 Request body: {body.decode('utf-8', errors='replace')}")
    logger.info(f"🔐 Signature header: {x_obot_signature_256}")
    
    # Validate signature
    if not validate_signature(body, x_obot_signature_256, SECRET):
        logger.error("❌ Invalid webhook signature")
        raise HTTPException(status_code=401, detail="Invalid signature")
    
    try:
        # Parse message
        message = WebhookMessage.model_validate_json(body)
        logger.info(f"✅ Processing {message.method} message")
        
        # Check all requests for suspicious content
        check_message_for_threats(message)
        
        logger.info(f"🎉 Webhook processed successfully: {message.method}")
        return {"status": "accepted", "message": "Webhook processed successfully"}
        
    except HTTPException:
        # Re-raise HTTP exceptions (like 403 from threat detection)
        raise
    except Exception as e:
        logger.error(f"Error processing webhook: {e}")
        raise HTTPException(status_code=400, detail=f"Invalid payload: {str(e)}")

def check_message_for_threats(message: WebhookMessage) -> None:
    """
    Check DuckDuckGo search requests for unsafe query content.
    
    This example specifically looks for DuckDuckGo search tool usage
    and filters based on the search query content.
    
    Args:
        message: JSON-RPC message to check
        
    Raises:
        HTTPException: 403 status if unsafe search query detected
    """
    logger.info(f"Checking message for threats: {message.method}")
    
    # Look for DuckDuckGo search tool calls
    if message.method == "tools/call" and message.params:
        tool_name = message.params.get("name")
        arguments = message.params.get("arguments", {})
        
        if tool_name == "search" and "query" in arguments:
            query = arguments["query"]
            logger.info(f"🔍 Checking DuckDuckGo search query: '{query}'")
            
            if is_unsafe_search_query(query):
                logger.error(f"🚫 UNSAFE SEARCH QUERY BLOCKED: '{query}'")
                raise HTTPException(
                    status_code=403, 
                    detail="Search query rejected due to content policy"
                )
            else:
                logger.info(f"✅ Safe search query: '{query}'")
    
    logger.debug(f"✅ Clean message: {message.method}")

def is_unsafe_search_query(query: str) -> bool:
    """
    Check if a DuckDuckGo search query contains unsafe content.
    
    Args:
        query: The search query string
        
    Returns:
        True if query should be blocked, False if safe
    """
    if not query or not isinstance(query, str):
        return False
    
    query_lower = query.lower()
    
    # Simple list of terms we don't want to allow in searches
    unsafe_terms = [
        "how to hack",
        "how to exploit", 
        "malware download",
        "virus download",
        "illegal drugs",
        "how to make bomb",
        "assassination",
        "terrorist",
    ]
    
    for term in unsafe_terms:
        if term in query_lower:
            logger.warning(f"Found unsafe search term: '{term}'")
            return True
    
    # Block excessively long queries (potential injection attempts)
    if len(query) > 200:
        logger.warning(f"Query too long: {len(query)} chars")
        return True
    
    return False

@app.get("/health")
async def health_check():
    """Simple health check endpoint."""
    return {"status": "healthy", "filter": "ready"}

@app.get("/")
async def root():
    """Root endpoint with basic information."""
    return {
        "message": "Simple Webhook with Tool Filtering",
        "endpoints": {
            "/webhook": "Main webhook endpoint (POST)",
            "/health": "Health check (GET)",
            "/": "This information (GET)"
        },
        "note": "Send JSON-RPC messages to /webhook with proper signatures. Suspicious content will result in 403 responses."
    }

def main():
    """Start the webhook server."""
    import uvicorn
    
    logger.info("🚀 Starting Simple Webhook Server with Content Filtering")
    logger.info(f"🌐 Host: 0.0.0.0")
    logger.info(f"🔌 Port: {PORT}")
    logger.info(f"🔐 Secret configured: {'Yes' if SECRET != 'test_secret' else 'Using default (change WEBHOOK_SECRET)'}")
    logger.info(f"📋 Available endpoints:")
    logger.info(f"   POST /webhook - Main webhook endpoint")
    logger.info(f"   GET /health - Health check")
    logger.info(f"   GET / - Server information")
    
    uvicorn.run(
        app,
        host="0.0.0.0",
        port=PORT,
        log_level="info"
    )

if __name__ == "__main__":
    main()
```
