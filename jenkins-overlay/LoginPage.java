package pages;

import static com.codeborne.selenide.Condition.text;
import static com.codeborne.selenide.Condition.visible;
import static com.codeborne.selenide.Selenide.$;
import static com.codeborne.selenide.Selenide.open;
import static com.codeborne.selenide.WebDriverConditions.url;
import static com.codeborne.selenide.Selenide.webdriver;

import config.ConfigReader;
import com.codeborne.selenide.SelenideElement;
import io.qameta.allure.Step;

import java.time.Duration;

public class LoginPage {

    private final SelenideElement loginForm = $("[data-testid='login-form']");
    private final SelenideElement embeddedHeader = $("[data-testid='header']");
    private final SelenideElement loginInput = $("[data-testid='login-input']");
    private final SelenideElement passwordInput = $("[data-testid='password-input']");
    private final SelenideElement submitButton = $("[data-testid='submit-button']");
    private final SelenideElement formTitle = $("[data-testid='login-form-title']");
    private final SelenideElement errorMessage = $("[data-testid='error-message']");
    private final SelenideElement registerLink = $("[data-testid='register-link']");

    @Step("Open login page")
    public LoginPage openPage() {
        if (ConfigReader.testConfig.skipOpen()) {
            loginForm.shouldBe(visible, Duration.ofSeconds(2));
            return this;
        }
        open("/login");
        return this;
    }

    @Step("Click 'Register' link under the login form")
    public RegisterPage clickRegisterLink() {
        registerLink.click();
        return new RegisterPage();
    }

    @Step("Fill and submit form")
    public HomePage fillAndSubmitForm(String username, String password) {
        typeUsername(username);
        typePassword(password);
        return submit();
    }

    @Step("Type username: {username}")
    public LoginPage typeUsername(String username) {
        loginInput.setValue(username);
        return this;
    }

    @Step("Type password")
    public LoginPage typePassword(String password) {
        passwordInput.setValue(password);
        return this;
    }

    @Step("Submit login form")
    public HomePage submit() {
        submitButton.click();
        webdriver().shouldHave(url(ConfigReader.resolveBaseUrl()));
        return new HomePage();
    }

    @Step("Submit login form expecting validation error")
    public LoginPage submitExpectingError() {
        submitButton.click();
        errorMessage.shouldBe(visible, Duration.ofSeconds(10));
        return this;
    }

    @Step("Verify embedded header is mounted")
    public LoginPage shouldShowEmbeddedHeader() {
        embeddedHeader.shouldBe(visible, Duration.ofSeconds(10));
        return this;
    }

    @Step("Verify login form is mounted")
    public LoginPage shouldShowLoginForm() {
        formTitle.shouldBe(visible);
        loginInput.shouldBe(visible);
        passwordInput.shouldBe(visible);
        submitButton.shouldBe(visible);
        return this;
    }

    @Step("Verify form title message: {message}")
    public LoginPage shouldHaveFormTitle(String message) {
        formTitle.shouldHave(text(message));
        return this;
    }

    @Step("Verify error message: {message}")
    public LoginPage shouldHaveErrorMessage(String message) {
        errorMessage.shouldHave(text(message));
        return this;
    }
}
