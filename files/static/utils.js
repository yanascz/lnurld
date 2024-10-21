function element(elementId) {
    return document.getElementById(elementId)
}

function reloadPage() {
    document.location.reload()
}

function navigateTo(uri) {
    location.href = uri
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
