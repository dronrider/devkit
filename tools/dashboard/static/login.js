// Вход: токен уходит в /api/login, дальше работает кука. Ошибка рисуется
// текстом, как и всё на этих экранах.
const form = document.getElementById("login-form");
const errBox = document.getElementById("login-error");

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  errBox.textContent = "";
  const token = document.getElementById("token").value.trim();
  if (!token) {
    errBox.textContent = "токен пустой";
    return;
  }
  try {
    const resp = await fetch("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token }),
    });
    if (resp.ok) {
      location.href = "/";
      return;
    }
    const body = await resp.json().catch(() => ({}));
    errBox.textContent = body.error || ("вход не прошёл (" + resp.status + ")");
  } catch (err) {
    errBox.textContent = "сервер не ответил: " + err;
  }
});
