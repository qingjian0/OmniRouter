#include <jni.h>

extern int OmnirouterStart(char *data_dir, char *gateway_key, char *encryption_key);
extern int OmnirouterStop(void);
extern int OmnirouterPair(char *token, int token_len, char *cookie, int cookie_len);

JNIEXPORT jint JNICALL
Java_com_qingjian0_omnirouter_NativeBridge_nativeStart(
    JNIEnv *env, jobject thiz, jstring data_dir, jstring gateway_key, jstring encryption_key
) {
    (void) thiz;
    const char *data = (*env)->GetStringUTFChars(env, data_dir, 0);
    const char *gateway = (*env)->GetStringUTFChars(env, gateway_key, 0);
    const char *encryption = (*env)->GetStringUTFChars(env, encryption_key, 0);
    if (data == 0 || gateway == 0 || encryption == 0) {
        if (data != 0) (*env)->ReleaseStringUTFChars(env, data_dir, data);
        if (gateway != 0) (*env)->ReleaseStringUTFChars(env, gateway_key, gateway);
        if (encryption != 0) (*env)->ReleaseStringUTFChars(env, encryption_key, encryption);
        return -10;
    }
    int result = OmnirouterStart((char *) data, (char *) gateway, (char *) encryption);
    (*env)->ReleaseStringUTFChars(env, data_dir, data);
    (*env)->ReleaseStringUTFChars(env, gateway_key, gateway);
    (*env)->ReleaseStringUTFChars(env, encryption_key, encryption);
    return (jint) result;
}


JNIEXPORT jint JNICALL
Java_com_qingjian0_omnirouter_NativeBridge_nativePair(
    JNIEnv *env, jobject thiz, jbyteArray token, jbyteArray cookie
) {
    (void) thiz;
    if (token == 0 || cookie == 0) return -40;
    jsize token_len = (*env)->GetArrayLength(env, token);
    jsize cookie_len = (*env)->GetArrayLength(env, cookie);
    jbyte *token_bytes = (*env)->GetByteArrayElements(env, token, 0);
    jbyte *cookie_bytes = (*env)->GetByteArrayElements(env, cookie, 0);
    if (token_bytes == 0 || cookie_bytes == 0) {
        if (token_bytes != 0) (*env)->ReleaseByteArrayElements(env, token, token_bytes, JNI_ABORT);
        if (cookie_bytes != 0) (*env)->ReleaseByteArrayElements(env, cookie, cookie_bytes, JNI_ABORT);
        return -40;
    }
    int result = OmnirouterPair((char *) token_bytes, (int) token_len, (char *) cookie_bytes, (int) cookie_len);
    (*env)->ReleaseByteArrayElements(env, token, token_bytes, JNI_ABORT);
    (*env)->ReleaseByteArrayElements(env, cookie, cookie_bytes, JNI_ABORT);
    return (jint) result;
}

JNIEXPORT jint JNICALL
Java_com_qingjian0_omnirouter_NativeBridge_nativeStop(JNIEnv *env, jobject thiz) {
    (void) env;
    (void) thiz;
    return (jint) OmnirouterStop();
}
