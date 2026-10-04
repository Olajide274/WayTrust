const API = "http://localhost:8080";

function $(id) {
  return document.getElementById(id);
}

function setMsg(text, ok) {
  const msgEl = $("msg");
  if (!msgEl) return;
  msgEl.style.color = ok ? "#8dffb0" : "#ffb4b4";
  msgEl.textContent = text || "";
}

function token() {
  return localStorage.getItem("nurtw_token") || "";
}

function naira(kobo) {
  return (Number(kobo || 0) / 100).toLocaleString("en-NG", {
    style: "currency",
    currency: "NGN",
  });
}

async function loadParks() {
  const origin = $("originPark");
  const dest = $("destPark");
  if (!origin || !dest) return;
  const res = await fetch(API + "/api/v1/parks");
  if (!res.ok) throw new Error("Could not load parks");
  const parks = await res.json();
  const opts = (parks || [])
    .map(function (p) {
      return (
        '<option value="' +
        p.id +
        '">' +
        (p.name || p.id) +
        " (" +
        (p.city || "") +
        ")</option>"
      );
    })
    .join("");
  origin.innerHTML = opts;
  dest.innerHTML = opts;
  if (parks && parks.length > 1) dest.selectedIndex = 1;
}

async function loadWaybills() {
  const waybillListEl = $("waybillList");
  if (!waybillListEl) return;
  waybillListEl.innerHTML = "<p class='sub'>Loading…</p>";
  const res = await fetch(API + "/api/v1/agent/waybills", {
    headers: { Authorization: "Bearer " + token() },
  });
  if (!res.ok) {
    const err = await res.json().catch(function () {
      return {};
    });
    throw new Error(err.error || "Could not load waybills");
  }
  const items = await res.json();
  if (!items || items.length === 0) {
    waybillListEl.innerHTML = "<p class='sub'>No waybills yet</p>";
    return;
  }
  waybillListEl.innerHTML = items
    .map(function (w) {
      return (
        '<div class="waybill-item"><strong>' +
        (w.id || "") +
        "</strong> · " +
        (w.current_state || "") +
        "<br/>" +
        naira(w.total_freight_kobo) +
        " · " +
        (w.origin_park_id || "") +
        " → " +
        (w.destination_park_id || "") +
        "</div>"
      );
    })
    .join("");
}

async function loadProfile() {
  const res = await fetch(API + "/api/v1/agent/profile", {
    headers: { Authorization: "Bearer " + token() },
  });
  if (!res.ok) throw new Error("Session expired — login again");
  const agent = await res.json();
  if ($("balance")) $("balance").textContent = naira(agent.wallet_balance_kobo);
  if ($("agentName")) {
    $("agentName").textContent =
      (agent.full_name || "") + " · " + (agent.park_id || "");
  }
  if ($("loginPanel")) $("loginPanel").classList.add("hidden");
  if ($("home")) $("home").classList.remove("hidden");
  await loadParks();
  await loadWaybills();
}

async function resolveDriver() {
  const account = ($("driverAccount") && $("driverAccount").value.trim()) || "";
  const bank = ($("driverBank") && $("driverBank").value.trim()) || "";
  const declared = ($("driverName") && $("driverName").value.trim()) || "Driver";
  const res = await fetch(API + "/api/v1/bank/resolve", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: "Bearer " + token(),
    },
    body: JSON.stringify({
      account_number: account,
      bank_code: bank,
      declared_name: declared,
    }),
  });
  const data = await res.json().catch(function () {
    return {};
  });
  if (!res.ok) throw new Error(data.message || data.error || "Resolve failed");
  return data;
}

async function bookWaybill() {
  const freightNaira = Number(($("freightNaira") && $("freightNaira").value) || 0);
  const body = {
    sender_phone: ($("senderPhone") && $("senderPhone").value.trim()) || "",
    receiver_phone: ($("receiverPhone") && $("receiverPhone").value.trim()) || "",
    driver_phone: ($("driverPhone") && $("driverPhone").value.trim()) || "",
    vehicle_reg_number: ($("vehicleReg") && $("vehicleReg").value.trim()) || "",
    origin_park_id: ($("originPark") && $("originPark").value) || "",
    destination_park_id: ($("destPark") && $("destPark").value) || "",
    total_freight_kobo: Math.round(freightNaira * 100),
    driver_account_number: ($("driverAccount") && $("driverAccount").value.trim()) || "",
    driver_bank_code: ($("driverBank") && $("driverBank").value.trim()) || "",
    driver_declared_name: ($("driverName") && $("driverName").value.trim()) || "",
  };
  const res = await fetch(API + "/api/v1/waybills", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: "Bearer " + token(),
    },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(function () {
    return {};
  });
  if (!res.ok) throw new Error(data.error || "Booking failed");
  return data;
}

async function verifyPin() {
  const waybillId = ($("pinWaybillId") && $("pinWaybillId").value.trim()) || "";
  const pin = ($("pinCode") && $("pinCode").value.trim()) || "";
  const res = await fetch(API + "/api/v1/waybills/verify-pin", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: "Bearer " + token(),
    },
    body: JSON.stringify({
      waybill_id: waybillId,
      pin: pin,
    }),
  });
  const data = await res.json().catch(function () {
    return {};
  });
  if (!res.ok) throw new Error(data.error || data.message || "PIN verification failed");
  return data;
}

function bind() {
  const loginBtn = $("loginBtn");
  const logoutBtn = $("logoutBtn");
  const refreshWaybillsBtn = $("refreshWaybillsBtn");
  const bookBtn = $("bookBtn");
  const resolveBtn = $("resolveBtn");
  const verifyPinBtn = $("verifyPinBtn");

  if (loginBtn) {
    loginBtn.addEventListener("click", async function () {
      setMsg("");
      loginBtn.disabled = true;
      try {
        const res = await fetch(API + "/api/v1/agent/login", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            phone_number: ($("phone") && $("phone").value.trim()) || "",
            password: ($("password") && $("password").value) || "",
          }),
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || "Login failed");
        localStorage.setItem("nurtw_token", data.token);
        setMsg("Login successful", true);
        await loadProfile();
      } catch (e) {
        setMsg(e.message || "Network error");
        if ($("home")) $("home").classList.add("hidden");
        if ($("loginPanel")) $("loginPanel").classList.remove("hidden");
      } finally {
        loginBtn.disabled = false;
      }
    });
  }

  if (logoutBtn) {
    logoutBtn.addEventListener("click", function () {
      localStorage.removeItem("nurtw_token");
      if ($("home")) $("home").classList.add("hidden");
      if ($("loginPanel")) $("loginPanel").classList.remove("hidden");
      if ($("waybillList")) $("waybillList").innerHTML = "";
      if ($("bookResult")) $("bookResult").textContent = "";
      if ($("resolveResult")) $("resolveResult").textContent = "";
      if ($("pinResult")) $("pinResult").textContent = "";
      setMsg("Logged out", true);
    });
  }

  if (refreshWaybillsBtn) {
    refreshWaybillsBtn.addEventListener("click", function () {
      loadWaybills().catch(function (e) {
        setMsg(e.message);
      });
    });
  }

  if (resolveBtn) {
    resolveBtn.addEventListener("click", async function () {
      setMsg("");
      resolveBtn.disabled = true;
      if ($("resolveResult")) $("resolveResult").textContent = "Resolving…";
      try {
        const data = await resolveDriver();
        const name = data.resolved_name || data.ResolvedAccountName || "";
        if (name && $("driverName")) $("driverName").value = name;
        if ($("resolveResult")) {
          $("resolveResult").textContent =
            (data.verified ? "Verified: " : "Result: ") +
            name +
            (data.confidence_score != null
              ? " (score " + data.confidence_score + ")"
              : "");
        }
        setMsg(data.verified ? "Bank account resolved" : "Check name match", !!data.verified);
      } catch (e) {
        if ($("resolveResult")) $("resolveResult").textContent = "";
        setMsg(e.message || "Resolve failed");
      } finally {
        resolveBtn.disabled = false;
      }
    });
  }

  if (bookBtn) {
    bookBtn.addEventListener("click", async function () {
      setMsg("");
      bookBtn.disabled = true;
      if ($("bookResult")) $("bookResult").textContent = "Booking…";
      try {
        const data = await bookWaybill();
        const pin = data.secret_pin || data.SecretPin || "";
        const id = data.waybill_id || data.WaybillID || data.id || "";
        if ($("bookResult")) {
          $("bookResult").textContent =
            "Booked " + id + (pin ? " · PIN " + pin : "") + " (show PIN only to receiver)";
        }
        if (id && $("pinWaybillId")) $("pinWaybillId").value = id;
        if (pin && $("pinCode")) $("pinCode").value = pin;
        setMsg("Waybill locked in escrow", true);
        await loadProfile();
      } catch (e) {
        if ($("bookResult")) $("bookResult").textContent = "";
        setMsg(e.message || "Booking failed");
      } finally {
        bookBtn.disabled = false;
      }
    });
  }

  if (verifyPinBtn) {
    verifyPinBtn.addEventListener("click", async function () {
      setMsg("");
      verifyPinBtn.disabled = true;
      if ($("pinResult")) $("pinResult").textContent = "Verifying…";
      try {
        const data = await verifyPin();
        if ($("pinResult")) {
          $("pinResult").textContent =
            (data.message || "PIN verified") +
            (data.waybill_id ? " · " + data.waybill_id : "") +
            (data.state || data.current_state
              ? " · " + (data.state || data.current_state)
              : "");
        }
        setMsg("Parcel marked DELIVERED", true);
        await loadWaybills();
      } catch (e) {
        if ($("pinResult")) $("pinResult").textContent = "";
        setMsg(e.message || "PIN verification failed");
      } finally {
        verifyPinBtn.disabled = false;
      }
    });
  }

  if (token()) {
    loadProfile().catch(function () {
      localStorage.removeItem("nurtw_token");
      if ($("loginPanel")) $("loginPanel").classList.remove("hidden");
    });
  }
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", bind);
} else {
  bind();
}