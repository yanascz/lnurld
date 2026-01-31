function element(elementId) {
    return document.getElementById(elementId)
}

function reloadPage() {
    document.location.reload()
}

function navigateTo(uri) {
    location.href = uri
}

function writeTextToClipboard(text, button) {
    navigator.clipboard.writeText(text)
    setTimeout(() => button.innerText = 'Copy to clipboard', 2000)
    button.innerText = '✓ Copied to clipboard'
}

function post(uri, body = {}) {
    return fetch(uri, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            'Accept': 'application/json'
        },
        body: JSON.stringify(body)
    })
}

function put(uri, body) {
    return fetch(uri, {
        method: 'PUT',
        headers: {
            'Content-Type': 'application/json',
            'Accept': 'application/json'
        },
        body: JSON.stringify(body)
    })
}
