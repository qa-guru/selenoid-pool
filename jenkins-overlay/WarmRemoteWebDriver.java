package helpers;

import org.openqa.selenium.WebDriverException;
import org.openqa.selenium.remote.HttpCommandExecutor;
import org.openqa.selenium.remote.RemoteWebDriver;
import org.openqa.selenium.remote.codec.w3c.W3CHttpCommandCodec;
import org.openqa.selenium.remote.codec.w3c.W3CHttpResponseCodec;

import java.lang.reflect.Field;
import java.net.URI;
import java.net.URL;

/**
 * Attach to an already-running WebDriver session (warm-pool preopen).
 * Does not call NewSession — chromedriver keeps a single hot session.
 */
public final class WarmRemoteWebDriver extends RemoteWebDriver {

    private WarmRemoteWebDriver() {
        super();
    }

    public static RemoteWebDriver attach(String remoteUrl, String sessionId) {
        if (remoteUrl == null || remoteUrl.isBlank()) {
            throw new WebDriverException("warm attach: remoteUrl is blank");
        }
        if (sessionId == null || sessionId.isBlank()) {
            throw new WebDriverException("warm attach: sessionId is blank");
        }
        try {
            String normalized = remoteUrl.endsWith("/") ? remoteUrl : remoteUrl + "/";
            URL url = URI.create(normalized).toURL();
            HttpCommandExecutor executor = new HttpCommandExecutor(url);

            // HttpCommandExecutor only wires W3C codecs after NewSession — set them manually.
            setField(executor, "commandCodec", new W3CHttpCommandCodec());
            setField(executor, "responseCodec", new W3CHttpResponseCodec());

            WarmRemoteWebDriver driver = new WarmRemoteWebDriver();
            driver.setCommandExecutor(executor);
            driver.setSessionId(sessionId);

            driver.getCurrentUrl();
            return driver;
        } catch (Exception e) {
            throw new WebDriverException(
                    "warm attach failed: remoteUrl=" + remoteUrl + " sessionId=" + sessionId, e);
        }
    }

    private static void setField(Object target, String name, Object value) throws Exception {
        Field field = target.getClass().getDeclaredField(name);
        field.setAccessible(true);
        field.set(target, value);
    }
}
