package tests;

import com.codeborne.selenide.Configuration;
import com.codeborne.selenide.WebDriverRunner;
import com.codeborne.selenide.logevents.SimpleReport;

import allure.AllureSelenideListeners;
import allure.Attachments;
import annotations.Framework;
import annotations.Scope;
import config.ConfigReader;
import config.TestConfig;
import helpers.BrowserSessionHelper;
import helpers.HarCapture;
import helpers.LocalChromePin;
import helpers.WarmRemoteWebDriver;
import pages.HomePage;
import pages.LoginPage;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.TestInfo;
import org.openqa.selenium.MutableCapabilities;
import org.openqa.selenium.chrome.ChromeOptions;

import java.util.HashMap;

import static com.codeborne.selenide.Selenide.closeWebDriver;


@Scope("browser")
@Framework("selenide")
public class TestBase {

    protected HomePage homePage = new HomePage();
    protected LoginPage loginPage = new LoginPage();
    
    protected static final TestConfig config = ConfigReader.testConfig;
    private static final SimpleReport selenideReport = new SimpleReport();

    private static boolean allureResultsEnabled() {
        return !"none".equals(config.allureReportMode());
    }

    @BeforeAll
    static void setup() {
        if (config.logToConsole()) {
            System.setProperty("org.slf4j.simpleLogger.defaultLogLevel", config.rootLogLevel());
        } else {
            System.setProperty("org.slf4j.simpleLogger.defaultLogLevel", "off");
        }

        Configuration.baseUrl = ConfigReader.resolveWebBaseUrl();
        Configuration.browser = config.browser();
        Configuration.browserSize = config.browserSize();
        Configuration.headless = config.headless();

        boolean captureHar = config.enableHar() || config.attachHarLogs();
        boolean warmAttach = !config.warmSessionId().isBlank();

        if (warmAttach) {
            // Attach to preopened warm session — do not set Configuration.remote (would NewSession).
            Configuration.remote = null;
            WebDriverRunner.setWebDriver(
                    WarmRemoteWebDriver.attach(config.remoteUrl(), config.warmSessionId()));
        } else if (!config.remoteUrl().isBlank()) {
            Configuration.browserVersion = config.browserVersion();
            Configuration.remote = config.remoteUrl();
            var selenoidOpts = new HashMap<String, Object>();
            selenoidOpts.put("enableVNC", config.enableVnc());
            selenoidOpts.put("enableVideo", config.enableVideo());
            selenoidOpts.put("headless", config.headless());
            // enableHar is client-side (HarCapture); do not send fake hub capability
            var capabilities = new MutableCapabilities();
            capabilities.setCapability("selenoid:options", selenoidOpts);
            if (captureHar && HarCapture.supportsBrowser(config.browser())) {
                HarCapture.enablePerformanceLogging(capabilities);
            }
            Configuration.browserCapabilities = capabilities;
        } else if ("chrome".equals(config.browser())) {
            LocalChromePin.apply(config.browserVersion());
        } else {
            Configuration.browserVersion = config.browserVersion();
        }

        if (!warmAttach && config.remoteUrl().isBlank()) {
            ChromeOptions chrome = new ChromeOptions();
            if (config.headless()) {
                chrome.addArguments("--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage");
            }
            if (captureHar && HarCapture.supportsBrowser(config.browser())) {
                HarCapture.enablePerformanceLogging(chrome);
            }
            if (config.headless() || captureHar) {
                Configuration.browserCapabilities = chrome;
            }
        }

        if (AllureSelenideListeners.isGloballyEnabled(config)) {
            AllureSelenideListeners.setEnabled(true);
        }
    }

    @BeforeEach
    void beforeEach() {
        if (config.logToConsole() && config.selenideLogToConsole()) {
            selenideReport.start();
        }
        // Warm preopen path: leave the already-loaded login page alone.
        if (!config.skipOpen()
                && !config.closeBrowserAfterEach()
                && WebDriverRunner.hasWebDriverStarted()) {
            BrowserSessionHelper.resetPageState();
        }
    }

    @AfterEach
    void afterEach(TestInfo testInfo) {
        if (config.logToConsole() && config.selenideLogToConsole()) {
            selenideReport.finish(testInfo.getDisplayName());
        }

        if (!allureResultsEnabled()) {
            if (config.closeBrowserAfterEach()) {
                closeWebDriver();
            }
            return;
        }

        if (config.attachBrowserConsoleLogs()) {
            Attachments.browserConsoleLogs();
        }
        if (config.attachPageSource()) {
            Attachments.pageSource();
        }

        if (config.attachHarLogs()) {
            Attachments.harLogs();
        }

        if (config.attachLastScreenshot()) {
            Attachments.screenshot("Last screenshot");
        }

        if (config.enableVideo() && config.attachVideo()) {
            Attachments.video();
        }
        if (config.closeBrowserAfterEach()) {
            closeWebDriver();
        }   
    }

    @AfterAll
    static void afterAll() {
        if (config.closeBrowserAfterAll() && WebDriverRunner.hasWebDriverStarted()) {
            closeWebDriver();
        }
    }

}
