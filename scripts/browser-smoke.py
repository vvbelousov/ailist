"""Run against a disposable, empty AIList instance."""
import os

from playwright.sync_api import expect, sync_playwright

with sync_playwright() as runtime:
    browser = runtime.chromium.launch(executable_path=os.getenv("CHROMIUM") or None)
    page = browser.new_page(locale="en-US")
    page.goto(os.getenv("APP_URL", "http://127.0.0.1:8080"))
    page.get_by_label("Access token").fill(os.environ["APP_TOKEN"])
    page.get_by_role("button", name="Sign in").click()
    page.get_by_role("button", name="Manual entry").click()
    page.get_by_label("Name", exact=True).fill("smoke rice")
    page.get_by_label("Quantity", exact=True).fill("2")
    page.get_by_label("Unit", exact=True).fill("kg")
    page.get_by_role("button", name="Review", exact=True).click()
    page.get_by_role("button", name="Confirm").click()
    expect(page.get_by_role("button", name="smoke rice Food 2 kg")).to_be_visible()
    page.reload()
    expect(page.get_by_role("button", name="smoke rice Food 2 kg")).to_be_visible()
    page.get_by_role("button", name="History").click()
    expect(page.locator("article").filter(has_text="smoke rice")).to_be_visible()
    page.get_by_role("button", name="RU", exact=True).click()
    expect(page.get_by_role("heading", name="Домашние запасы")).to_be_visible()
    page.get_by_role("button", name="EN", exact=True).click()
    ops = [dict(action="add", name=name, quantity=1, unit="pcs",
                location="pantry", category="food", expiry="")
           for name in ("flour", "sausage", "tomato", "soap", "potato")]
    page.route("**/api/interpret", lambda route: route.fulfill(json={"mode": "write", "ops": ops}))
    page.get_by_label("Inventory assistant").fill("five items")
    page.get_by_role("button", name="Interpret").click()
    page.get_by_role("button", name="Edit row 3").click()
    expect(page.get_by_role("button", name="Confirm")).to_be_disabled()
    page.get_by_label("Name", exact=True).fill("discard me")
    page.get_by_role("button", name="Discard edit").click()
    page.get_by_role("button", name="Edit row 3").click()
    expect(page.get_by_label("Name", exact=True)).to_have_value("tomato")
    page.get_by_label("Name", exact=True).fill("tomatoes")
    page.get_by_label("Quantity", exact=True).fill("2")
    page.get_by_role("button", name="Save row").click()
    expect(page.get_by_role("listitem")).to_have_count(5)
    page.get_by_role("button", name="Confirm").click()
    for name in ("flour", "sausage", "tomatoes", "soap", "potato"):
        quantity = 2 if name == "tomatoes" else 1
        expect(page.get_by_role("button", name=f"{name} Food · pantry {quantity} pcs")).to_be_visible()
    browser.close()
